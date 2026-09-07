package ts3

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	teamspeak "github.com/honeybbq/teamspeak-go"
	"github.com/tsukiyoz/resona/internal/iconcache"
)

func TestIconWorkersReuseConnectorCacheAcrossReconnectAndRestart(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "resona", "icons")
	c := New(filepath.Join(t.TempDir(), "identity.key"))
	c.icons = iconcache.New(dir)
	data, err := rasterIconDataURL(testIcon(t, "png", 16))
	if err != nil {
		t.Fatal(err)
	}
	var downloads atomic.Int32
	scope := iconcache.Key{Protocol: "ts3", ServerUID: "server-uid", Address: "example:9987", Endpoint: "127.0.0.1:9987", IdentityUID: "local-user-uid"}
	load := func(connector *Connector) {
		t.Helper()
		ctx, cancel := context.WithCancel(context.Background())
		published := make(chan string, 1)
		loader := newCachedIconLoader(ctx, connector.icons, func() iconcache.Key { return scope },
			func(context.Context, string) (string, error) { downloads.Add(1); return data, nil },
			func(id, got string) {
				if id == "1000" {
					published <- got
				}
			})
		defer func() { cancel(); loader.Close() }()
		loader.Request("1000")
		select {
		case got := <-published:
			if got != data {
				t.Fatal("worker published incorrect cached image")
			}
		case <-time.After(time.Second):
			t.Fatal("worker failed to publish")
		}
	}
	load(c)
	load(c)
	if downloads.Load() != 1 {
		t.Fatal("reconnect downloaded cached icon")
	}
	restarted := New("unused-identity")
	restarted.icons = iconcache.New(dir)
	load(restarted)
	if downloads.Load() != 1 {
		t.Fatal("restart downloaded disk-cached icon")
	}
	scope.ServerUID = "another-server"
	load(restarted)
	if downloads.Load() != 2 {
		t.Fatal("server identity failed to isolate icon")
	}
}

func TestInitServerCachesServerIdentitySeparatelyFromClientIdentity(t *testing.T) {
	r := newReducer("local-user")
	r.apply(teamspeak.IncomingCommand{Name: "initserver", Params: map[string]string{
		"aclid": "1", "virtualserver_unique_identifier": "remote-server", "virtualserver_name": "Test",
	}})
	if r.serverUID != "remote-server" || r.state.IdentityUID != "local-user" {
		t.Fatalf("server identity %q mixed with local identity %q", r.serverUID, r.state.IdentityUID)
	}
	r = newReducer("local-user")
	r.apply(teamspeak.IncomingCommand{Name: "initserver", Params: map[string]string{"aclid": "1"}})
	if r.serverUID != "" {
		t.Fatal("missing server identity replaced by client identity")
	}
}

func TestExplicitConnectorCachesDoNotShareMemory(t *testing.T) {
	first, second := New("unused-a"), New("unused-b")
	if first.icons == nil || first.icons == second.icons {
		t.Fatal("explicit connectors must own independent caches")
	}
}

func TestConnectorConstructorsKeepExplicitPathsInMemoryAndDefaultOnDisk(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	dir, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	dir = filepath.Join(dir, "resona", "icons")
	data, err := rasterIconDataURL(testIcon(t, "png", 16))
	if err != nil {
		t.Fatal(err)
	}
	key := iconcache.Key{Protocol: "ts3", Address: "localhost:9987", IconID: "1000", IdentityUID: "test", TransformVersion: iconTransformVersion}
	downloads := 0
	fetch := func(context.Context) (string, error) { downloads++; return data, nil }
	explicit := New(filepath.Join(home, "identity.key"))
	for range 2 {
		if _, err := explicit.icons.Get(context.Background(), key, fetch); err != nil {
			t.Fatal(err)
		}
	}
	if downloads != 1 {
		t.Fatal("explicit connector does not share L1 across accesses")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("explicit connector unexpectedly writes default cache")
	}
	for range 2 {
		connector, err := NewDefault()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := connector.icons.Get(context.Background(), key, fetch); err != nil {
			t.Fatal(err)
		}
	}
	if downloads != 2 {
		t.Fatalf("default cache should fetch once across new instances: %d", downloads)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatal("default connector did not persist in OS cache directory:", err)
	}
}
