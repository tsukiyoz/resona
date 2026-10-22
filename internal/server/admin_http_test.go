package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testAdminOrigin = "http://127.0.0.1:9989"

func adminRequest(a http.Handler, method, path, body, csrf string, cookie *http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, testAdminOrigin+adminBase+path, strings.NewReader(body))
	r.Header.Set("Origin", testAdminOrigin)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-CSRF-Token", csrf)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	return w
}

func testAdminLogin(t *testing.T, s *Server, owner string) (*AdminConsole, *http.Cookie, string) {
	t.Helper()
	_, secret, err := s.config.Store.IssueAdminCredential(context.Background(), owner, owner, "Test", 24*time.Hour, 0)
	if err != nil {
		t.Fatal(err)
	}
	a, err := NewAdminConsole(s, testAdminOrigin)
	if err != nil {
		t.Fatal(err)
	}
	w := adminRequest(a, "POST", "/login", `{"token":"`+secret+`"}`, "", nil)
	var result struct {
		CSRF string `json:"csrf"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.CSRF == "" {
		t.Fatal(w.Code, w.Body.String())
	}
	cookie := w.Result().Cookies()[0]
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatal("unsafe cookie")
	}
	return a, cookie, result.CSRF
}

func TestAdminSessionOriginCSRFExpiryAndRevocation(t *testing.T) {
	s, owner, _ := channelTestServer(t)
	a, cookie, csrf := testAdminLogin(t, s, owner.identity)
	if w := adminRequest(a, "GET", "/session", "", "", nil); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := adminRequest(a, "GET", "/session", "", "", cookie); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := adminRequest(a, "POST", "/logout", `{}`, "wrong", cookie); w.Code != 403 {
		t.Fatal(w.Code)
	}
	r := httptest.NewRequest("POST", testAdminOrigin+adminBase+"/logout", strings.NewReader(`{}`))
	r.Header.Set("Origin", "https://evil.example")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("cross-origin accepted")
	}
	r = httptest.NewRequest("GET", "http://evil.example"+adminBase+"/session", nil)
	r.AddCookie(cookie)
	w = httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("untrusted host accepted")
	}
	if w := adminRequest(a, "POST", "/logout", `{}`, csrf, cookie); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := adminRequest(a, "GET", "/session", "", "", cookie); w.Code != 401 {
		t.Fatal("logout failed")
	}
	a, cookie, _ = testAdminLogin(t, s, owner.identity)
	credential := s.config.Store.snapshot().Credentials[0]
	if err := s.config.Store.RevokeAdminCredential(context.Background(), owner.identity, credential.ID, 0); err != nil {
		t.Fatal(err)
	}
	// Revoke the credential used by the current session, not an older login.
	for _, c := range s.config.Store.snapshot().Credentials {
		if err := s.config.Store.RevokeAdminCredential(context.Background(), owner.identity, c.ID, 0); err != nil {
			t.Fatal(err)
		}
	}
	if w := adminRequest(a, "GET", "/session", "", "", cookie); w.Code != 401 {
		t.Fatal("revoked credential still accepted")
	}
	a, cookie, _ = testAdminLogin(t, s, owner.identity)
	key := sessionKey(cookie.Value)
	session := a.sessions[key]
	session.Expires = time.Now().Add(-time.Second)
	a.sessions[key] = session
	if w := adminRequest(a, "GET", "/session", "", "", cookie); w.Code != 401 {
		t.Fatal("expired session accepted")
	}
}

func TestAdminCredentialPersistenceDoesNotStoreSecretAndRoleRevokesSession(t *testing.T) {
	s, owner, dir := channelTestServer(t)
	admin := strings.Repeat("b", 64)
	ctx := context.Background()
	if err := s.config.Store.SetRole(ctx, owner.identity, admin, "admin", 0); err != nil {
		t.Fatal(err)
	}
	c, secret, err := s.config.Store.IssueAdminCredential(ctx, owner.identity, admin, "Admin laptop", time.Hour, 0)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := s.config.Store.kv.Get(ctx, resourceKey)
	if err != nil || bytes.Contains(entry.Value(), []byte(secret)) {
		t.Fatal("plaintext secret stored", err)
	}
	s.config.Store.Close()
	s.config.Store = testStore(t, dir)
	if got, ok := s.config.Store.authenticateAdmin(secret); !ok || got.ID != c.ID {
		t.Fatal("credential lost after restart")
	}
	if err := s.config.Store.SetRole(ctx, owner.identity, admin, "member", 0); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.config.Store.authenticateAdmin(secret); ok {
		t.Fatal("revoked admin can login")
	}
	if _, err := NewAdminConsole(s, "http://0.0.0.0:9989"); err == nil {
		t.Fatal("public plaintext origin accepted")
	}
}

func TestAdminLoginRejectsMalformedAndRateLimits(t *testing.T) {
	s, _, _ := channelTestServer(t)
	a, _ := NewAdminConsole(s, testAdminOrigin)
	if w := adminRequest(a, "POST", "/login", `{"token":"wrong","identity":"owner"}`, "", nil); w.Code != 400 {
		t.Fatal(w.Code)
	}
	if w := adminRequest(a, "POST", "/login", `{"token":"wrong"}`, "", nil); w.Code != 401 {
		t.Fatal(w.Code)
	}
	for range 4 {
		adminRequest(a, "POST", "/login", `{"token":"wrong"}`, "", nil)
	}
	if w := adminRequest(a, "POST", "/login", `{}`, "", nil); w.Code != 429 {
		t.Fatal(w.Code)
	}
}
