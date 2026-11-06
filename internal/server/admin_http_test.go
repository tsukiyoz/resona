package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const (
	testAdminOrigin     = "http://127.0.0.1:9989"
	testAccountPassword = "test account password"
)

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
	account, err := s.config.Store.accounts.Account(context.Background(), owner)
	if err != nil {
		t.Fatal(err)
	}
	a, err := NewAdminConsole(s, testAdminOrigin)
	if err != nil {
		t.Fatal(err)
	}
	w := adminRequest(a, "POST", "/login", `{"username":"`+account.Username+`","password":"`+testAccountPassword+`"}`, "", nil)
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
	_, err := s.config.Store.ResetAccountPassword(context.Background(), "owner", testAccountPassword)
	if err != nil {
		t.Fatal(err)
	}
	if w := adminRequest(a, "GET", "/session", "", "", cookie); w.Code != 401 {
		t.Fatal("old password version still accepted")
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

func TestAdminLoginCapacityAndCookieReplacement(t *testing.T) {
	s, owner, _ := channelTestServer(t)
	a, cookie, _ := testAdminLogin(t, s, owner.principal())
	for i := 0; i < 63; i++ {
		a.sessions[fmt.Sprintf("occupied-%d", i)] = adminSession{Expires: time.Now().Add(time.Hour), Done: make(chan struct{})}
	}
	login := `{"username":"owner","password":"` + testAccountPassword + `"}`
	if w := adminRequest(a, "POST", "/login", login, "", nil); w.Code != 503 || len(a.sessions) != 64 {
		t.Fatal("capacity exceeded", w.Code, len(a.sessions))
	}
	oldDone := a.sessions[sessionKey(cookie.Value)].Done
	if w := adminRequest(a, "POST", "/login", login, "", cookie); w.Code != 200 || len(a.sessions) != 64 {
		t.Fatal("cookie replacement failed", w.Code, len(a.sessions))
	}
	select {
	case <-oldDone:
	default:
		t.Fatal("replaced session still live")
	}
}

func TestAdminRoleRevokesAccountSession(t *testing.T) {
	s, owner, _ := channelTestServer(t)
	ctx := context.Background()
	account, err := s.config.Store.accounts.Register(ctx, "admin", testAccountPassword, "Administrator")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.config.Store.SetRole(ctx, owner.identity, account.UID, "admin", 0); err != nil {
		t.Fatal(err)
	}
	a, cookie, _ := testAdminLogin(t, s, account.UID)
	if err := s.config.Store.SetRole(ctx, owner.identity, account.UID, "member", 0); err != nil {
		t.Fatal(err)
	}
	if w := adminRequest(a, "GET", "/session", "", "", cookie); w.Code != 401 {
		t.Fatal("revoked admin still authenticated")
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
	if w := adminRequest(a, "POST", "/login", `{"token":"wrong"}`, "", nil); w.Code != 400 {
		t.Fatal(w.Code)
	}
	for range 4 {
		adminRequest(a, "POST", "/login", `{"username":"wrong","password":"wrong"}`, "", nil)
	}
	if w := adminRequest(a, "POST", "/login", `{}`, "", nil); w.Code != 429 {
		t.Fatal(w.Code)
	}
}
