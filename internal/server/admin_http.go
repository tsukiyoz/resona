package server

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const adminBase = "/api/admin/v1"

type adminSession struct {
	Identity, Credential, CSRF string
	Expires                    time.Time
	Done                       chan struct{}
}

// AdminConsole owns bounded in-memory browser sessions; it creates no background
// workers or NATS consumers. The caller owns the HTTP server's lifetime.
type AdminConsole struct {
	server      *Server
	origin      *url.URL
	mux         *http.ServeMux
	mu          sync.Mutex
	sessions    map[string]adminSession
	loginBucket bucket
}

func NewAdminConsole(s *Server, origin string) (*AdminConsole, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New("invalid admin origin")
	}
	if u.Scheme == "http" && u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" && u.Hostname() != "::1" {
		return nil, errors.New("public admin origin requires HTTPS")
	}
	u.Path = ""
	a := &AdminConsole{server: s, origin: u, mux: http.NewServeMux(), sessions: make(map[string]adminSession)}
	a.mux.HandleFunc("POST "+adminBase+"/login", a.login)
	a.mux.HandleFunc("POST "+adminBase+"/logout", a.logout)
	a.mux.HandleFunc("GET "+adminBase+"/session", a.sessionInfo)
	a.registerResources()
	a.registerMembers()
	a.mux.HandleFunc("PUT "+adminBase+"/settings", a.settings)
	return a, nil
}

func (a *AdminConsole) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
	if r.Host != a.origin.Host || (r.Method != "GET" && r.Header.Get("Origin") != a.origin.String()) {
		adminError(w, http.StatusForbidden, "请求来源不受信任")
		return
	}
	a.mux.ServeHTTP(w, r)
}

func adminJSON(w http.ResponseWriter, code int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(value)
}

func adminError(w http.ResponseWriter, code int, message string) {
	adminJSON(w, code, map[string]string{"error": message})
}

func adminDecode(w http.ResponseWriter, r *http.Request, into any) bool {
	if ct := r.Header.Get("Content-Type"); ct != "application/json" && !strings.HasPrefix(ct, "application/json;") {
		adminError(w, http.StatusUnsupportedMediaType, "请求必须使用 JSON")
		return false
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
	d.DisallowUnknownFields()
	var extra any
	if d.Decode(into) != nil || d.Decode(&extra) != io.EOF {
		adminError(w, http.StatusBadRequest, "请求内容无效或过大")
		return false
	}
	return true
}

func sessionKey(secret string) string {
	digest := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(digest[:])
}

func (a *AdminConsole) cookie(value string, expires time.Time) *http.Cookie {
	return &http.Cookie{Name: "resona_admin", Value: value, Path: adminBase + "/", HttpOnly: true, Secure: a.origin.Scheme == "https", SameSite: http.SameSiteStrictMode, Expires: expires}
}

func (a *AdminConsole) login(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	allowed := a.loginBucket.take(1, 5)
	a.mu.Unlock()
	if !allowed {
		adminError(w, http.StatusTooManyRequests, "登录过于频繁，请稍后再试")
		return
	}
	var v struct {
		Token string `json:"token"`
	}
	if !adminDecode(w, r, &v) {
		return
	}
	c, ok := a.server.config.Store.authenticateAdmin(v.Token)
	if !ok {
		adminError(w, http.StatusUnauthorized, "管理凭据无效、已过期或权限已撤销")
		return
	}
	expires := minTime(c.Expires, time.Now().Add(8*time.Hour))
	secret := rand.Text() + rand.Text()
	session := adminSession{Identity: c.Identity, Credential: c.ID, CSRF: rand.Text(), Expires: expires, Done: make(chan struct{})}
	a.mu.Lock()
	for key, previous := range a.sessions {
		if !time.Now().Before(previous.Expires) {
			close(previous.Done)
			delete(a.sessions, key)
		}
	}
	if len(a.sessions) >= 64 {
		a.mu.Unlock()
		adminError(w, http.StatusServiceUnavailable, "管理会话已满，请稍后再试")
		return
	}
	if old, err := r.Cookie("resona_admin"); err == nil {
		if previous, ok := a.sessions[sessionKey(old.Value)]; ok {
			close(previous.Done)
		}
		delete(a.sessions, sessionKey(old.Value))
	}
	a.sessions[sessionKey(secret)] = session
	a.mu.Unlock()
	http.SetCookie(w, a.cookie(secret, expires))
	adminJSON(w, http.StatusOK, map[string]any{"identity": session.Identity, "csrf": session.CSRF, "expires": session.Expires})
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func (a *AdminConsole) authenticate(w http.ResponseWriter, r *http.Request) (adminSession, bool) {
	cookie, err := r.Cookie("resona_admin")
	if err != nil {
		adminError(w, 401, "请登录管理后台")
		return adminSession{}, false
	}
	key := sessionKey(cookie.Value)
	a.mu.Lock()
	s, ok := a.sessions[key]
	if ok && !time.Now().Before(s.Expires) {
		close(s.Done)
		delete(a.sessions, key)
		ok = false
	}
	a.mu.Unlock()
	if !ok || !a.server.config.Store.adminSessionValid(s.Identity, s.Credential) {
		adminError(w, 401, "登录已过期或权限已撤销，请重新登录")
		return adminSession{}, false
	}
	if r.Method != "GET" && r.Header.Get("X-CSRF-Token") != s.CSRF {
		adminError(w, 403, "登录状态已改变，请刷新页面")
		return adminSession{}, false
	}
	return s, true
}

func (a *AdminConsole) logout(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticate(w, r); !ok {
		return
	}
	cookie, _ := r.Cookie("resona_admin")
	a.mu.Lock()
	if session, ok := a.sessions[sessionKey(cookie.Value)]; ok {
		close(session.Done)
	}
	delete(a.sessions, sessionKey(cookie.Value))
	a.mu.Unlock()
	expired := a.cookie("", time.Unix(1, 0))
	expired.MaxAge = -1
	http.SetCookie(w, expired)
	adminJSON(w, 200, map[string]bool{"ok": true})
}

func (a *AdminConsole) sessionInfo(w http.ResponseWriter, r *http.Request) {
	if s, ok := a.authenticate(w, r); ok {
		adminJSON(w, 200, map[string]any{"identity": s.Identity, "csrf": s.CSRF, "expires": s.Expires})
	}
}
