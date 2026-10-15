package updates

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/tsukiyoz/resona/internal/version"
)

const checkTimeout = 10 * time.Second

type Status struct {
	State             string `json:"state"`
	Version           string `json:"version,omitempty"`
	Notes             string `json:"notes,omitempty"`
	Error             string `json:"error,omitempty"`
	Distribution      string `json:"distribution"`
	CanDownload       bool   `json:"canDownload"`
	CanInstall        bool   `json:"canInstall"`
	CanOpenRelease    bool   `json:"canOpenRelease"`
	ReleaseURL        string `json:"releaseURL,omitempty"`
	Configured        bool   `json:"configured"`
	UnavailableReason string `json:"unavailableReason,omitempty"`
}

type Manager struct {
	mu                                             sync.Mutex
	status                                         Status
	cancel                                         context.CancelFunc
	done                                           chan struct{}
	endpoint, key, current, platform, distribution string
	http                                           *http.Client
	closed                                         bool
}

func New() *Manager {
	executable, _ := os.Executable()
	platform := runtime.GOOS + "/" + runtime.GOARCH
	current := version.Current().Version
	return &Manager{
		status: Status{State: "idle"}, distribution: packageDistribution(executable, platform, current),
		endpoint: ManifestURL, key: PublicKey, current: current, platform: platform,
		http: &http.Client{Timeout: checkTimeout, CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 || !httpsURL(req.URL.String()) {
				return errors.New("更新服务跳转无效")
			}
			return nil
		}},
	}
}

func (m *Manager) Status() Status { m.mu.Lock(); defer m.mu.Unlock(); return m.statusLocked() }

func (m *Manager) statusLocked() Status {
	status := m.status
	status.Distribution = m.distribution
	status.CanDownload, status.CanInstall = false, false
	status.UnavailableReason = m.unavailableReason()
	status.Configured = status.UnavailableReason == ""
	status.CanOpenRelease = status.Configured && status.State == "available" && status.ReleaseURL != ""
	return status
}

func (m *Manager) unavailableReason() string {
	if m.closed {
		return "更新检查已关闭"
	}
	switch m.distribution {
	case DistributionPortable:
	case DistributionInstaller:
		return "安装器更新尚未接通"
	case "":
		return "此构建未标记有效分发方式"
	default:
		return "不支持的分发方式"
	}
	if !httpsURL(m.endpoint) || m.key == "" {
		return "此构建尚未配置正式更新源和签名公钥"
	}
	if _, err := versionParts(m.current); err != nil {
		return "开发版本不检查正式更新，请使用正式发布包"
	}
	return ""
}

func (m *Manager) ReleasePage() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || !m.statusLocked().CanOpenRelease {
		return "", errors.New("请先检查可用更新")
	}
	return m.status.ReleaseURL, nil
}

// One bounded worker checks metadata; there are no package writes or idle timers.
func (m *Manager) Start(parent context.Context, notify func(Status)) error {
	m.mu.Lock()
	if m.closed || m.done != nil {
		m.mu.Unlock()
		return errors.New("更新检查正在进行或已关闭")
	}
	if reason := m.unavailableReason(); reason != "" {
		m.mu.Unlock()
		return errors.New(reason)
	}
	ctx, cancel := context.WithTimeout(parent, checkTimeout)
	done := make(chan struct{})
	m.cancel, m.done = cancel, done
	m.status = Status{State: "checking"}
	initial := m.statusLocked()
	m.mu.Unlock()
	notify(initial)
	go func() {
		defer cancel()
		manifest, err := m.check(ctx)
		m.mu.Lock()
		if err == nil && ctx.Err() != nil {
			err = ctx.Err()
		}
		if err != nil {
			m.status = Status{State: "failed", Error: err.Error()}
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				m.status.Error = "连接更新服务超时，请检查网络后重试"
			} else if errors.Is(ctx.Err(), context.Canceled) {
				m.status = Status{State: "cancelled", Error: "已取消检查更新"}
			}
		} else {
			newer, _ := Newer(manifest.Version, m.current)
			m.status = Status{State: "current", Version: manifest.Version, Notes: manifest.Notes, ReleaseURL: manifest.ReleaseURL}
			if newer {
				m.status.State = "available"
			}
		}
		status := m.statusLocked()
		m.mu.Unlock()
		notify(status)
		m.mu.Lock()
		m.done, m.cancel = nil, nil
		close(done)
		m.mu.Unlock()
	}()
	return nil
}

func (m *Manager) Cancel() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel != nil {
		m.cancel()
	}
}

func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	done := m.done
	if m.cancel != nil {
		m.cancel()
	}
	m.mu.Unlock()
	if done != nil {
		<-done
	}
}

func (m *Manager) check(ctx context.Context) (Manifest, error) {
	var empty Manifest
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.endpoint, nil)
	if err != nil {
		return empty, errors.New("更新检查地址无效")
	}
	req.Header.Set("User-Agent", "Resona-Updater/1")
	response, err := m.http.Do(req)
	if err != nil {
		return empty, updateNetworkError(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		switch response.StatusCode {
		case http.StatusNotFound:
			return empty, errors.New("尚未发布更新清单（HTTP 404），请稍后重试")
		case http.StatusForbidden:
			return empty, errors.New("更新服务拒绝访问（HTTP 403）")
		case http.StatusTooManyRequests:
			return empty, errors.New("更新请求过于频繁（HTTP 429），请稍后重试")
		default:
			return empty, fmt.Errorf("更新服务响应异常（HTTP %d），请稍后重试", response.StatusCode)
		}
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	if err != nil {
		var networkError net.Error
		if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &networkError) {
			return empty, updateNetworkError(err)
		}
		return empty, errors.New("更新清单读取失败")
	}
	manifest, err := Verify(data, m.key, time.Now())
	if err != nil {
		return empty, err
	}
	if _, err := Newer(manifest.Version, m.current); err != nil {
		return empty, err
	}
	asset, ok := manifest.Assets[m.platform]
	if !ok || asset.Distribution != m.distribution {
		return empty, errors.New("此版本没有当前平台和分发方式的更新包")
	}
	return manifest, nil
}

func updateNetworkError(err error) error {
	var networkError net.Error
	var dnsError *net.DNSError
	var certificateError *tls.CertificateVerificationError
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &networkError) && networkError.Timeout():
		return errors.New("连接更新服务超时，请检查网络后重试")
	case errors.As(err, &dnsError):
		return errors.New("无法解析更新服务器地址，请检查网络或 DNS 设置")
	case errors.As(err, &certificateError):
		return errors.New("更新服务安全连接验证失败，请检查系统时间或网络代理")
	default:
		return errors.New("无法连接更新服务，请检查网络或代理设置")
	}
}
