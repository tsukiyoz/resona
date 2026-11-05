package server

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	w "github.com/tsukiyoz/resona/internal/nativewire"
)

func TestOwnerPasswordChangesPersistAndStayOutOfSnapshots(t *testing.T) {
	s, owner, _ := channelTestServer(t)
	if err := s.config.Store.SetConnectionPassword(context.Background(), "initial-password"); err != nil {
		t.Fatal(err)
	}
	state, err := s.config.Store.readPassword(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	s.passwordAuth, s.passwordStatus = state.auth, state.status()
	initial := s.passwordStatus.Revision
	a, cookie, csrf := testAdminLogin(t, s, owner.identity)
	body := `{"revision":"` + initial + `","password":"new-password"}`
	if got := adminRequest(a, "PUT", "/server-password", body, "wrong", cookie); got.Code != 403 {
		t.Fatal("CSRF accepted", got.Code)
	}
	if err := s.SetConnectionPassword(context.Background(), strings.Repeat("b", 64), "new-password", initial); !errors.Is(err, ErrPermission) {
		t.Fatal("non-owner changed password", err)
	}
	if got := adminRequest(a, "PUT", "/server-password", body, csrf, cookie); got.Code != 200 || strings.Contains(got.Body.String(), "new-password") {
		t.Fatal(got.Code, got.Body.String())
	}
	if err := s.SetConnectionPassword(context.Background(), owner.identity, "other-password", initial); !errors.Is(err, ErrConflict) {
		t.Fatal("stale edit accepted", err)
	}
	state, err = s.config.Store.readPassword(context.Background())
	if err != nil || state.revision != s.passwordStatus.Revision {
		t.Fatal("restart would lose revision", err)
	}
	checkStoredPassword(t, state.auth, "new-password", true)
	if got := adminRequest(a, "GET", "/state", "", "", cookie); strings.Contains(got.Body.String(), "new-password") || strings.Contains(got.Body.String(), "initial-password") {
		t.Fatal("snapshot contains password")
	}
	reset := adminRequest(a, "POST", "/server-password", `{"revision":"`+s.passwordStatus.Revision+`"}`, csrf, cookie)
	var result struct {
		Password string `json:"password"`
	}
	if reset.Code != 200 || json.Unmarshal(reset.Body.Bytes(), &result) != nil || len(result.Password) < 24 {
		t.Fatal("random reset failed", reset.Code)
	}
	state, err = s.config.Store.readPassword(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	checkStoredPassword(t, state.auth, result.Password, true)
	before := s.passwordAuth
	if err := s.SetConnectionPassword(context.Background(), owner.identity, "", s.passwordStatus.Revision); !errors.Is(err, ErrInvalid) || s.passwordAuth != before {
		t.Fatal("empty password accepted")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.SetConnectionPassword(canceled, owner.identity, "canceled-password", s.passwordStatus.Revision); !errors.Is(err, context.Canceled) || s.passwordAuth != before {
		t.Fatal("canceled operation replaced active authentication", err)
	}
}

func TestPasswordRotationPreservesMembersAndRejectsPendingOldLogin(t *testing.T) {
	store := testStore(t, t.TempDir())
	claim, err := store.ProvisionOwnerClaim(false)
	if err != nil {
		t.Fatal(err)
	}
	owner := strings.Repeat("a", 64)
	if err := store.Claim(owner, claim); err != nil {
		t.Fatal(err)
	}
	if err := store.SetConnectionPassword(context.Background(), "old-password"); err != nil {
		t.Fatal(err)
	}
	s, err := Listen("127.0.0.1:0", Config{Store: store, Name: "Password test", NoiseKey: []byte(strings.Repeat("k", 32))})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("server failed to stop")
		}
	})
	dial := func(password string) w.Connection {
		t.Helper()
		c, err := w.DialServer(ctx, s.Addr().String(), nil, password)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.CloseWithError(0, "test done") })
		return c
	}
	hello := func(c w.Connection) (w.Stream, error) {
		t.Helper()
		stream, err := c.OpenStreamSync(ctx)
		if err != nil {
			return nil, err
		}
		_ = stream.SetDeadline(time.Now().Add(2 * time.Second))
		_, key, _ := ed25519.GenerateKey(rand.Reader)
		h := w.Hello{Nickname: "Test"}
		if err := w.SignHello(c, key, &h); err != nil {
			t.Fatal(err)
		}
		if err := w.Write(stream, w.HelloKind, 0, h); err != nil {
			return stream, err
		}
		f, err := w.Read(stream)
		if err == nil && f.Kind != w.WelcomeKind {
			return stream, errors.New("missing welcome")
		}
		return stream, err
	}
	live := dial("old-password")
	if _, err := hello(live); err != nil {
		t.Fatal(err)
	}
	pending := dial("old-password")
	s.mu.Lock()
	revision := s.passwordStatus.Revision
	s.mu.Unlock()
	if err := s.SetConnectionPassword(ctx, owner, "new-password", revision); err != nil {
		t.Fatal(err)
	}
	if _, err := hello(pending); err == nil {
		t.Fatal("old PAKE admitted after rotation")
	}
	if c, err := w.DialServer(ctx, s.Addr().String(), nil, "old-password"); err == nil {
		_ = c.CloseWithError(0, "test done")
		t.Fatal("old password accepted")
	}
	if _, err := hello(dial("new-password")); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	count := len(s.peers)
	s.mu.Unlock()
	if live.Context().Err() != nil || count != 2 {
		t.Fatal("rotation interrupted an established member", count)
	}
}
