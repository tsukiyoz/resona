package updates

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/tsukiyoz/resona/internal/version"
)

type Status struct {
	State      string `json:"state"`
	Version    string `json:"version,omitempty"`
	Notes      string `json:"notes,omitempty"`
	Error      string `json:"error,omitempty"`
	CanInstall bool   `json:"canInstall"`
	Configured bool   `json:"configured"`
}

type Manager struct {
	mu                               sync.Mutex
	status                           Status
	cancel                           context.CancelFunc
	done                             chan struct{}
	manifest                         Manifest
	signed                           []byte
	directory                        string
	cacheRoot                        string
	endpoint, key, current, platform string
	http                             *http.Client
	closed                           bool
}

func New() *Manager {
	return &Manager{status: Status{State: "idle"}, endpoint: ManifestURL, key: PublicKey, current: version.Current().Version, platform: runtime.GOOS + "/" + runtime.GOARCH, http: &http.Client{Timeout: 5 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || !httpsURL(req.URL.String()) {
			return errors.New("更新下载跳转无效")
		}
		return nil
	}}}
}

func (m *Manager) Status() Status { m.mu.Lock(); defer m.mu.Unlock(); return m.statusLocked() }
func (m *Manager) statusLocked() Status {
	status := m.status
	status.Configured = httpsURL(m.endpoint) && m.key != ""
	return status
}

func (m *Manager) PackageDirectory() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.status.State != "ready" || m.directory == "" {
		return "", errors.New("更新包尚未就绪")
	}
	return m.directory, nil
}

// Exactly one worker performs network/disk work. There is no idle timer.
func (m *Manager) Start(parent context.Context, download bool, notify func(Status)) error {
	m.mu.Lock()
	if m.closed || m.done != nil {
		m.mu.Unlock()
		return errors.New("更新任务正在进行或已关闭")
	}
	if m.status.State == "installing" {
		m.mu.Unlock()
		return errors.New("更新安装已准备退出")
	}
	if download && m.status.State != "available" {
		m.mu.Unlock()
		return errors.New("请先检查可用更新")
	}
	if !httpsURL(m.endpoint) || m.key == "" {
		m.mu.Unlock()
		return errors.New("此构建尚未配置正式更新源和签名公钥")
	}
	if !download {
		if _, err := versionParts(m.current); err != nil {
			m.mu.Unlock()
			return errors.New("开发版本不自动更新，请使用正式发布包")
		}
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Minute)
	m.cancel = cancel
	done := make(chan struct{})
	m.done = done
	m.status.State, m.status.Error = "checking", ""
	if download {
		m.status.State = "downloading"
	}
	initial := m.statusLocked()
	m.mu.Unlock()
	notify(initial)
	go func() {
		defer cancel()
		var err error
		if download {
			err = m.download(ctx)
		} else {
			err = m.check(ctx, false)
		}
		m.mu.Lock()
		if err != nil {
			m.status.State, m.status.Error = "failed", err.Error()
			if ctx.Err() != nil {
				m.status.State, m.status.Error = "cancelled", "更新任务已取消或超时"
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
	m.mu.Lock()
	directory := m.directory
	installing := m.status.State == "installing"
	m.mu.Unlock()
	if directory != "" && !installing {
		_ = os.RemoveAll(directory)
	}
}

func (m *Manager) fetch(ctx context.Context, endpoint string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, errors.New("更新下载地址无效")
	}
	req.Header.Set("User-Agent", "Resona-Updater/1")
	r, err := m.http.Do(req)
	if err != nil {
		return nil, errors.New("无法访问更新服务，请检查网络后重试")
	}
	if r.StatusCode != http.StatusOK {
		r.Body.Close()
		return nil, errors.New("更新服务暂不可用，请稍后重试")
	}
	return r, nil
}

func (m *Manager) check(ctx context.Context, downloading bool) error {
	r, err := m.fetch(ctx, m.endpoint)
	if err != nil {
		return err
	}
	defer r.Body.Close()
	data, err := io.ReadAll(io.LimitReader(r.Body, (64<<10)+1))
	if err != nil {
		return errors.New("更新清单下载失败")
	}
	manifest, err := Verify(data, m.key, time.Now())
	if err != nil {
		return err
	}
	newer, err := Newer(manifest.Version, m.current)
	if err != nil {
		return err
	}
	if _, ok := manifest.Assets[m.platform]; !ok {
		return errors.New("此版本尚无当前平台的更新包")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.manifest, m.signed = manifest, data
	m.status = Status{State: "current", Version: manifest.Version, Notes: manifest.Notes}
	if newer {
		m.status.State = "available"
	}
	if downloading && newer {
		m.status.State = "downloading"
	}
	return nil
}

func (m *Manager) download(ctx context.Context) error {
	// Refresh the signed feed before downloading; it may have changed since check.
	if err := m.check(ctx, true); err != nil {
		return err
	}
	m.mu.Lock()
	manifest, signed := m.manifest, m.signed
	available := m.status.State == "downloading"
	m.mu.Unlock()
	if !available {
		return nil
	}
	asset := manifest.Assets[m.platform]
	r, err := m.fetch(ctx, asset.URL)
	if err != nil {
		return err
	}
	defer r.Body.Close()
	root := m.cacheRoot
	if root == "" {
		cache, err := os.UserCacheDir()
		if err != nil {
			return errors.New("无法定位更新缓存")
		}
		root = filepath.Join(cache, "resona", "updates")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return errors.New("无法创建更新缓存")
	}
	directory, err := os.MkdirTemp(root, "package-")
	if err != nil {
		return errors.New("无法创建更新目录")
	}
	success := false
	defer func() {
		if !success {
			_ = os.RemoveAll(directory)
		}
	}()
	f, err := os.OpenFile(filepath.Join(directory, "package.zip"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return errors.New("无法写入更新包")
	}
	digest := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(f, digest), io.LimitReader(r.Body, asset.Size+1))
	syncErr := f.Sync()
	closeErr := f.Close()
	if copyErr != nil || syncErr != nil || closeErr != nil || ctx.Err() != nil {
		return errors.New("更新包下载中断")
	}
	if n != asset.Size || hex.EncodeToString(digest.Sum(nil)) != asset.SHA256 {
		return errors.New("更新包完整性验证失败")
	}
	if err := os.WriteFile(filepath.Join(directory, "manifest.json"), signed, 0o600); err != nil {
		return errors.New("无法保存签名清单")
	}
	m.mu.Lock()
	old := m.directory
	m.directory = directory
	m.status = Status{State: "ready", Version: manifest.Version, Notes: manifest.Notes, CanInstall: m.platform == "windows/amd64"}
	m.mu.Unlock()
	if old != "" {
		_ = os.RemoveAll(old)
	}
	success = true
	return nil
}
