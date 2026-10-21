package updates

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func signedForTest(t *testing.T, manifest Manifest, key ed25519.PrivateKey) []byte {
	t.Helper()
	payload, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(SignedManifest{Payload: payload, Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(key, payload))})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func testManifest() Manifest {
	return Manifest{Schema: 1, Product: "resona", Channel: "stable", Version: "v0.2.0", ReleaseURL: "https://github.com/tsukiyoz/resona/releases/tag/v0.2.0", Expires: time.Now().Add(time.Hour), Assets: map[string]Asset{"windows/amd64": {Distribution: DistributionPortable, URL: "https://example.invalid/app.zip", SHA256: hex.EncodeToString(make([]byte, 32)), Size: 10}}}
}

func TestManifestAuthenticityVersionAndExpiry(t *testing.T) {
	public, private, _ := ed25519.GenerateKey(rand.Reader)
	key := base64.StdEncoding.EncodeToString(public)
	manifest := testManifest()
	data := signedForTest(t, manifest, private)
	if _, err := Verify(data, key, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(bytes.Replace(data, []byte("v0.2.0"), []byte("v9.2.0"), 1), key, time.Now()); err == nil {
		t.Fatal("accepted forged payload")
	}
	for _, mutate := range []func(*Manifest){
		func(m *Manifest) { m.Expires = time.Now().Add(-time.Hour) },
		func(m *Manifest) { m.Product = "other" },
		func(m *Manifest) { m.Schema = 2 },
		func(m *Manifest) { m.Version = "v01.2.0" },
		func(m *Manifest) { m.ReleaseURL = "https://github.com/other/releases/tag/v0.2.0" },
		func(m *Manifest) {
			a := m.Assets["windows/amd64"]
			a.Distribution = "installer"
			m.Assets["windows/amd64"] = a
		},
		func(m *Manifest) {
			a := m.Assets["windows/amd64"]
			a.URL = "http://example.invalid/app.zip"
			m.Assets["windows/amd64"] = a
		},
	} {
		changed := testManifest()
		mutate(&changed)
		if _, err := Verify(signedForTest(t, changed, private), key, time.Now()); err == nil {
			t.Fatal("accepted invalid signed manifest")
		}
	}
	for _, pair := range [][2]string{{"v0.1.4", "v0.1.4"}, {"v0.1.3", "v0.1.4"}, {"v0.2.0", "v0.1.4-dev"}} {
		if newer, _ := Newer(pair[0], pair[1]); newer {
			t.Fatalf("accepted %v", pair)
		}
	}
	if newer, err := Newer("v0.1.10", "v0.1.9"); err != nil || !newer {
		t.Fatal("numeric ordering failed")
	}
}

func TestPortableChecksOnlyMetadataAndSupportsCancelRetryAndExit(t *testing.T) {
	public, private, _ := ed25519.GenerateKey(rand.Reader)
	var feed atomic.Value
	var blocked atomic.Bool
	var packageRequests atomic.Int32
	started := make(chan struct{}, 1)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/manifest" {
			packageRequests.Add(1)
			return
		}
		if blocked.Load() {
			started <- struct{}{}
			<-r.Context().Done()
			return
		}
		_, _ = w.Write(feed.Load().([]byte))
	}))
	defer server.Close()
	m := New()
	m.endpoint = server.URL + "/manifest"
	m.key = base64.StdEncoding.EncodeToString(public)
	m.current = "v0.1.4"
	m.platform = "windows/amd64"
	m.distribution = DistributionPortable
	m.http = server.Client()
	defer m.Close()
	manifest := testManifest()
	feed.Store(signedForTest(t, manifest, private))
	statuses := make(chan Status, 16)
	start := func() {
		t.Helper()
		if err := m.Start(context.Background(), func(s Status) { statuses <- s }); err != nil {
			t.Fatal(err)
		}
	}
	wait := func(state string) {
		t.Helper()
		timer := time.NewTimer(3 * time.Second)
		defer timer.Stop()
		for {
			select {
			case status := <-statuses:
				if status.State != state {
					continue
				}
				m.mu.Lock()
				done := m.done
				m.mu.Unlock()
				if done != nil {
					<-done
				}
				return
			case <-timer.C:
				t.Fatalf("waiting for %s: %+v", state, m.Status())
			}
		}
	}
	if _, err := m.ReleasePage(); err == nil {
		t.Fatal("offered release before verification")
	}
	start()
	wait("available")
	status := m.Status()
	if !status.CanOpenRelease || status.CanDownload || status.CanInstall || status.Distribution != DistributionPortable {
		t.Fatalf("wrong capabilities: %+v", status)
	}
	if page, err := m.ReleasePage(); err != nil || page != manifest.ReleaseURL {
		t.Fatalf("wrong verified page: %q, %v", page, err)
	}
	if packageRequests.Load() != 0 {
		t.Fatal("portable downloaded a package")
	}
	blocked.Store(true)
	start()
	<-started
	if _, err := m.ReleasePage(); err == nil {
		t.Fatal("old release leaked into a new check")
	}
	if err := m.Start(context.Background(), func(Status) {}); err == nil {
		t.Fatal("parallel check accepted")
	}
	m.Cancel()
	wait("cancelled")
	if m.Status().Error != "已取消检查更新" {
		t.Fatalf("wrong cancellation reason: %+v", m.Status())
	}
	feed.Store([]byte("invalid signature"))
	blocked.Store(false)
	start()
	wait("failed")
	if _, err := m.ReleasePage(); err == nil {
		t.Fatal("failed check exposed a release")
	}
	feed.Store(signedForTest(t, manifest, private))
	start()
	wait("available")
	m.current = "v0.2.0"
	start()
	wait("current")
	if m.Status().CanOpenRelease {
		t.Fatal("current version offered update action")
	}
	blocked.Store(true)
	start()
	<-started
	m.Close()
	if m.Status().CanOpenRelease {
		t.Fatal("closed updater advertised release action")
	}
	if _, err := m.ReleasePage(); err == nil {
		t.Fatal("closed updater offered release")
	}
}

func TestUpdateFailureReasons(t *testing.T) {
	for _, test := range []struct {
		name          string
		code          int
		parentTimeout bool
		clientTimeout bool
		want          string
	}{
		{name: "missing manifest", code: 404, want: "尚未发布更新清单（HTTP 404），请稍后重试"},
		{name: "forbidden", code: 403, want: "更新服务拒绝访问（HTTP 403）"},
		{name: "rate limited", code: 429, want: "更新请求过于频繁（HTTP 429），请稍后重试"},
		{name: "server failure", code: 503, want: "更新服务响应异常（HTTP 503），请稍后重试"},
		{name: "parent deadline", parentTimeout: true, want: "连接更新服务超时，请检查网络后重试"},
		{name: "client timeout", clientTimeout: true, want: "连接更新服务超时，请检查网络后重试"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if test.code != 0 {
					w.WriteHeader(test.code)
					return
				}
				<-r.Context().Done()
			}))
			defer server.Close()
			m := New()
			m.endpoint, m.key, m.current, m.distribution = server.URL, "configured", "v0.1.4", DistributionPortable
			m.http = server.Client()
			if test.clientTimeout {
				m.http.Timeout = 50 * time.Millisecond
			}
			defer m.Close()
			ctx := context.Background()
			if test.parentTimeout {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 50*time.Millisecond)
				defer cancel()
			}
			finished := make(chan Status, 1)
			if err := m.Start(ctx, func(status Status) {
				if status.State != "checking" {
					finished <- status
				}
			}); err != nil {
				t.Fatal(err)
			}
			select {
			case status := <-finished:
				if status.State != "failed" || status.Error != test.want || status.CanOpenRelease {
					t.Fatalf("unexpected failure: %+v", status)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("check did not finish")
			}
		})
	}
}

func TestUpdateNetworkErrorsDoNotExposeRawRequestDetails(t *testing.T) {
	for _, test := range []struct {
		err  error
		want string
	}{
		{&net.DNSError{Err: "private lookup details", Name: "private.invalid"}, "无法解析更新服务器地址，请检查网络或 DNS 设置"},
		{&tls.CertificateVerificationError{Err: errors.New("private certificate details")}, "更新服务安全连接验证失败，请检查系统时间或网络代理"},
		{errors.New("private request details"), "无法连接更新服务，请检查网络或代理设置"},
	} {
		wrapped := &url.Error{Op: "Get", URL: "https://private.invalid/?secret=value", Err: test.err}
		if got := updateNetworkError(wrapped).Error(); got != test.want {
			t.Fatalf("unexpected reason: %s", got)
		}
	}
}

func TestUnsupportedBuildsNeverCheck(t *testing.T) {
	for _, distribution := range []string{"", "unknown", DistributionInstaller, DistributionPortable} {
		t.Run(distribution, func(t *testing.T) {
			m := New()
			m.distribution = distribution
			m.endpoint = "https://example.invalid/manifest"
			m.key = "configured"
			m.current = "dev"
			if distribution != DistributionPortable {
				m.current = "v0.1.4"
			}
			if s := m.Status(); s.Configured || s.UnavailableReason == "" || s.CanDownload || s.CanInstall || s.CanOpenRelease {
				t.Fatalf("unsafe capabilities: %+v", s)
			}
			if err := m.Start(context.Background(), func(Status) { t.Fatal("started disabled updater") }); err == nil {
				t.Fatal("disabled build accepted check")
			}
		})
	}
}

func TestPackageIdentityAndDistribution(t *testing.T) {
	root := t.TempDir()
	executable := filepath.Join(root, "resona-core.exe")
	path := filepath.Join(root, "resona-package.json")
	for _, distribution := range []string{"portable", "installer", "unknown", ""} {
		data := []byte(`{"schema":1,"product":"resona","platform":"windows/amd64","version":"v0.2.0","distribution":"` + distribution + `"}`)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		got := packageDistribution(executable, "windows/amd64", "v0.2.0")
		want := distribution
		if got != want {
			t.Fatalf("distribution %q became %q", distribution, got)
		}
		if got := packageDistribution(executable, "windows/amd64", "v0.3.0"); got != "" {
			t.Fatal("mismatched version accepted")
		}
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if got := packageDistribution(executable, "windows/amd64", "v0.2.0"); got != "" {
		t.Fatal("missing metadata granted type")
	}
	macCore := filepath.Join(root, "Resona.app", "Contents", "MacOS", "resona-core")
	macMetadata := filepath.Join(root, "Resona.app", "Contents", "Resources", "resona-package.json")
	if err := os.MkdirAll(filepath.Dir(macMetadata), 0o700); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"schema":1,"product":"resona","platform":"darwin/arm64","version":"v0.2.0","distribution":"portable"}`)
	if err := os.WriteFile(macMetadata, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := packageDistribution(macCore, "darwin/arm64", "v0.2.0"); got != DistributionPortable {
		t.Fatal("macOS resources not read")
	}
}

func TestWindowsReleaseZipRequiresPortableIdentity(t *testing.T) {
	for _, distribution := range []string{"portable", "installer", ""} {
		for _, duplicate := range []bool{false, true} {
			var buffer bytes.Buffer
			writer := zip.NewWriter(&buffer)
			for _, name := range []string{"resona-desktop.exe", "resona_webrtc_apm.dll", ".resona-package", "resona-package.json"} {
				file, _ := writer.Create(name)
				content := "product"
				if name == "resona-package.json" {
					content = `{"schema":1,"product":"resona","platform":"windows/amd64","version":"v0.2.0","distribution":"` + distribution + `"}`
				}
				_, _ = file.Write([]byte(content))
			}
			if duplicate {
				file, _ := writer.Create(strings.ToUpper("resona-desktop.exe"))
				_, _ = file.Write([]byte("duplicate"))
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "package.zip")
			if err := os.WriteFile(path, buffer.Bytes(), 0o600); err != nil {
				t.Fatal(err)
			}
			err := ValidateWindowsArchive(path, "v0.2.0")
			if (err == nil) != (distribution == "portable" && !duplicate) {
				t.Fatalf("accepted distribution=%q duplicate=%v: %v", distribution, duplicate, err)
			}
		}
	}
}

func TestMacReleaseZipRequiresMatchingArchitectureAndDistribution(t *testing.T) {
	for _, distribution := range []string{"portable", "installer"} {
		var buffer bytes.Buffer
		writer := zip.NewWriter(&buffer)
		for _, name := range []string{"MacOS/resona-desktop", "Frameworks/libresona_webrtc_apm.dylib", "Resources/.resona-bundle", "Resources/resona-package.json"} {
			file, err := writer.Create("Resona.app/Contents/" + name)
			if err != nil {
				t.Fatal(err)
			}
			content := "product"
			if name == "Resources/resona-package.json" {
				content = `{"schema":1,"product":"resona","platform":"darwin/arm64","version":"v0.2.0","distribution":"` + distribution + `"}`
			}
			if _, err := file.Write([]byte(content)); err != nil {
				t.Fatal(err)
			}
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "app.zip")
		if err := os.WriteFile(path, buffer.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := ValidateMacArchive(path, "darwin/arm64", "v0.2.0"); (err == nil) != (distribution == "portable") {
			t.Fatalf("distribution=%s: %v", distribution, err)
		}
		if err := ValidateMacArchive(path, "darwin/amd64", "v0.2.0"); err == nil {
			t.Fatal("wrong architecture accepted")
		}
	}
}
