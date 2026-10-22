package server

import (
	"context"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	w "github.com/tsukiyoz/resona/internal/nativewire"
)

func TestServeWithAdminReleasesBothListenersOnCancellation(t *testing.T) {
	store := testStore(t, t.TempDir())
	s, err := Listen("127.0.0.1:0", Config{Store: store, Name: "test", NoiseKey: []byte(strings.Repeat("k", 32)), Channels: []w.Channel{{ID: 1, Name: "Lobby"}}})
	if err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := l.Addr().String()
	l.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.ServeWithAdmin(ctx, address, "") }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", address, 20*time.Millisecond)
		if err == nil {
			conn.Close()
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("HTTP listener did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("shutdown did not join workers")
	}
	reopened, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatal("HTTP listener leaked", err)
	}
	reopened.Close()
}

func TestAdminStaticAssetsAreLocalAndProtectedByOrigin(t *testing.T) {
	s, owner, _ := channelTestServer(t)
	a, _, _ := testAdminLogin(t, s, owner.identity)
	for _, asset := range []string{"index.html", "app.js", "app.css", "icons.svg", "logo.svg"} {
		// The web assets intentionally need no session; private API data does.
		path := asset
		if asset == "index.html" {
			path = ""
		}
		response := httptest.NewRecorder()
		a.ServeHTTP(response, httptest.NewRequest("GET", testAdminOrigin+"/admin/"+path, nil))
		if response.Code != 200 || response.Header().Get("Content-Security-Policy") == "" {
			t.Fatal(asset, response.Code)
		}
		data, err := adminAssets.ReadFile("web/" + asset)
		if err != nil || len(data) == 0 {
			t.Fatal(asset, err)
		}
		if asset == "index.html" && strings.Contains(string(data), "https://") {
			t.Fatal("runtime CDN dependency")
		}
	}
}
