package updates

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func TestManifestAuthenticityVersionAndExpiry(t *testing.T) {
	public, private, _ := ed25519.GenerateKey(rand.Reader)
	key := base64.StdEncoding.EncodeToString(public)
	manifest := Manifest{Schema: 1, Product: "resona", Channel: "stable", Version: "v0.2.0", Expires: time.Now().Add(time.Hour), Assets: map[string]Asset{"windows/amd64": {URL: "https://example.invalid/app.zip", SHA256: hex.EncodeToString(make([]byte, 32)), Size: 10}}}
	data := signedForTest(t, manifest, private)
	if _, err := Verify(data, key, time.Now()); err != nil {
		t.Fatal(err)
	}
	tampered := bytes.Replace(data, []byte("v0.2.0"), []byte("v9.2.0"), 1)
	if _, err := Verify(tampered, key, time.Now()); err == nil {
		t.Fatal("accepted forged payload")
	}
	for _, mutation := range []func(*Manifest){func(m *Manifest) { m.Expires = time.Now().Add(-time.Hour) }, func(m *Manifest) { m.Product = "other" }, func(m *Manifest) { m.Schema = 2 }, func(m *Manifest) { m.Version = "v01.2.0" }, func(m *Manifest) {
		m.Assets["windows/amd64"] = Asset{URL: "http://example.invalid", SHA256: hex.EncodeToString(make([]byte, 32)), Size: 10}
	}} {
		changed := manifest
		changed.Assets = map[string]Asset{"windows/amd64": manifest.Assets["windows/amd64"]}
		mutation(&changed)
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
		t.Fatal("numeric version ordering failed")
	}
}

func TestUpdateDownloadAndCancellation(t *testing.T) {
	public, private, _ := ed25519.GenerateKey(rand.Reader)
	packageData := []byte("complete package")
	digest := sha256.Sum256(packageData)
	var feed atomic.Value
	started := make(chan struct{}, 1)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/manifest" {
			_, _ = w.Write(feed.Load().([]byte))
			return
		}
		if r.URL.Path == "/blocked" {
			started <- struct{}{}
			<-r.Context().Done()
			return
		}
		_, _ = w.Write(packageData)
	}))
	defer server.Close()
	manifest := Manifest{Schema: 1, Product: "resona", Channel: "stable", Version: "v0.2.0", Expires: time.Now().Add(time.Hour), Assets: map[string]Asset{"windows/amd64": {URL: server.URL + "/package", SHA256: hex.EncodeToString(digest[:]), Size: int64(len(packageData))}}}
	m := New()
	m.endpoint = server.URL + "/manifest"
	m.key = base64.StdEncoding.EncodeToString(public)
	m.current = "v0.1.4"
	m.platform = "windows/amd64"
	m.http = server.Client()
	m.cacheRoot = t.TempDir()
	defer m.Close()
	statuses := make(chan Status, 16)
	start := func(download bool) {
		t.Helper()
		if err := m.Start(context.Background(), download, func(status Status) { statuses <- status }); err != nil {
			t.Fatal(err)
		}
	}
	wait := func(state string) {
		t.Helper()
		timer := time.NewTimer(time.Second)
		defer timer.Stop()
		for {
			select {
			case s := <-statuses:
				if s.State == state {
					m.mu.Lock()
					done := m.done
					m.mu.Unlock()
					if done != nil {
						<-done
					}
					return
				}
			case <-timer.C:
				t.Fatalf("timed out waiting %s: %+v", state, m.Status())
			}
		}
	}
	feed.Store(signedForTest(t, manifest, private))
	start(false)
	wait("available")
	start(true)
	wait("ready")
	directory, err := m.PackageDirectory()
	if err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(directory, "package.zip")); err != nil || !bytes.Equal(data, packageData) {
		t.Fatal("download incomplete")
	}
	manifest.Assets["windows/amd64"] = Asset{URL: server.URL + "/blocked", SHA256: hex.EncodeToString(digest[:]), Size: int64(len(packageData))}
	feed.Store(signedForTest(t, manifest, private))
	start(false)
	wait("available")
	start(true)
	<-started
	if err := m.Start(context.Background(), false, func(Status) {}); err == nil {
		t.Fatal("parallel updater accepted")
	}
	m.Cancel()
	wait("cancelled")
	entries, _ := os.ReadDir(m.cacheRoot)
	if len(entries) != 1 {
		t.Fatalf("cancel left partial files: %d", len(entries))
	}
}

func makeArchive(t *testing.T, names []string) string {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, name := range names {
		f, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		data := []byte("product")
		if name == "resona-package.json" {
			data = []byte(`{"schema":1,"product":"resona","platform":"windows/amd64","version":"v0.2.0"}`)
		}
		_, _ = f.Write(data)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "package.zip")
	if err := os.WriteFile(path, buffer.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestArchivePathsAndReplacementRollback(t *testing.T) {
	names := []string{"resona-desktop.exe", "resona-core.exe", "resona_webrtc_apm.dll", ".resona-package", "resona-package.json"}
	archive := makeArchive(t, names)
	if err := ValidateWindowsArchive(archive, "v0.2.0"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateWindowsArchive(archive, "v0.3.0"); err == nil {
		t.Fatal("mislabeled package accepted")
	}
	if err := extractPackage(archive, t.TempDir(), "v0.2.0"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"../outside", "C:evil", "a\\b", "NUL.txt", "a/../b", "a.", "servers.json", "resona-core.EXE"} {
		t.Run(bad, func(t *testing.T) {
			withBad := append(append([]string{}, names...), bad)
			if err := extractPackage(makeArchive(t, withBad), t.TempDir(), "v0.2.0"); err == nil {
				t.Fatal("unsafe archive accepted")
			}
		})
	}
	root := t.TempDir()
	target := filepath.Join(root, "installed")
	backup := target + ".previous"
	_ = os.Mkdir(target, 0o700)
	_ = os.WriteFile(filepath.Join(target, "product"), []byte("old"), 0o600)
	if err := replaceDirectory(target, filepath.Join(root, "missing"), backup); err == nil {
		t.Fatal("missing stage accepted")
	}
	if data, err := os.ReadFile(filepath.Join(target, "product")); err != nil || string(data) != "old" {
		t.Fatal("failed replacement lost original")
	}
	stage := filepath.Join(root, "stage")
	_ = os.Mkdir(stage, 0o700)
	_ = os.WriteFile(filepath.Join(stage, "product"), []byte("new"), 0o600)
	if err := replaceDirectory(target, stage, backup); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(backup, "product")); string(data) != "old" {
		t.Fatal("backup lost")
	}
	if data, _ := os.ReadFile(filepath.Join(target, "product")); string(data) != "new" {
		t.Fatal("whole package not switched")
	}
}

func TestInstallHandoffMarkers(t *testing.T) {
	directory := t.TempDir()
	plan := filepath.Join(directory, "install.json")
	if err := os.WriteFile(filepath.Join(directory, "commit"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if InstallCommitted(plan) {
		t.Fatal("unacknowledged handoff accepted")
	}
	if waitMarker(directory, "commit", time.Now().Add(-time.Second)) {
		t.Fatal("expired preparation accepted stale marker")
	}
	if err := os.WriteFile(filepath.Join(directory, "armed"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if !InstallCommitted(plan) {
		t.Fatal("acknowledged handoff not accepted")
	}
	if err := os.WriteFile(filepath.Join(directory, "abort"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if InstallCommitted(plan) || waitMarker(directory, "armed", time.Now().Add(time.Second)) {
		t.Fatal("aborted handoff accepted")
	}
	if packageMarker(directory) {
		t.Fatal("unmarked installation accepted")
	}
	if err := os.Mkdir(filepath.Join(directory, ".resona-package"), 0o700); err != nil {
		t.Fatal(err)
	}
	if packageMarker(directory) {
		t.Fatal("non-file package marker accepted")
	}
}
