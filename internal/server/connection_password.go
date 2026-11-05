package server

import (
	"context"
	"crypto/rand"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/tsukiyoz/resona/internal/serverauth"
)

// ConnectionPasswordStatus contains no password or password-derived verifier.
// Revision is an independent random edit token, persisted with each change.
type ConnectionPasswordStatus struct {
	Enabled  bool   `json:"enabled"`
	Editable bool   `json:"editable"`
	Revision string `json:"revision"`
}

func validConnectionPassword(password string) bool {
	return len(password) <= 1024 && utf8.ValidString(password) && !strings.ContainsAny(password, "\r\n\x00")
}

func (p passwordState) status() ConnectionPasswordStatus {
	if p.auth == nil {
		return ConnectionPasswordStatus{Enabled: true}
	}
	return ConnectionPasswordStatus{Enabled: p.auth.Enabled(), Editable: true, Revision: p.revision}
}

func (s *Server) SetConnectionPassword(ctx context.Context, actor, password, revision string) error {
	if !s.config.Store.Allowed(actor, "settings", "write", "*") {
		return ErrPermission
	}
	if len(password) < 8 || !validConnectionPassword(password) || revision == "" {
		return ErrInvalid
	}
	// Serialize durable changes, but never hold the voice routing lock during IO.
	s.passwordMu.Lock()
	defer s.passwordMu.Unlock()
	s.mu.Lock()
	current := s.passwordStatus.Revision
	before := passwordState{auth: s.passwordAuth, revision: current}
	s.mu.Unlock()
	if current != revision {
		return ErrConflict
	}
	auth, err := serverauth.New(password)
	if err != nil {
		return err
	}
	actual, err := s.config.Store.replacePassword(ctx, auth, before)
	s.mu.Lock()
	// Readback reconciles uncertain writes. Unknown storage state gates only
	// new admissions; existing members keep their voice/control connections.
	if actual.revision != current || actual.auth == nil {
		s.passwordAuth = actual.auth
	}
	s.passwordStatus = actual.status()
	s.signalAdminLocked()
	s.mu.Unlock()
	return err
}

func (a *AdminConsole) connectionPassword(wr http.ResponseWriter, r *http.Request) {
	session, ok := a.authenticate(wr, r)
	if !ok {
		return
	}
	var v struct {
		Revision string `json:"revision"`
		Password string `json:"password"`
	}
	if !adminDecode(wr, r, &v) {
		return
	}
	if r.Method == http.MethodPost {
		if v.Password != "" {
			adminFailure(wr, ErrInvalid)
			return
		}
		v.Password = rand.Text()
	}
	if err := a.server.SetConnectionPassword(r.Context(), session.Identity, v.Password, v.Revision); err != nil {
		adminFailure(wr, err)
		return
	}
	if r.Method == http.MethodPost {
		adminJSON(wr, 200, map[string]string{"password": v.Password})
	} else {
		adminJSON(wr, 200, map[string]bool{"ok": true})
	}
}
