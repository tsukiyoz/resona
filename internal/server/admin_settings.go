package server

import (
	"context"
	"net/http"
	"strings"
	"time"
)

type ServerSettings struct {
	Name              string `json:"name"`
	MaxClients        int    `json:"max_clients"`
	ChatMaxMessages   int64  `json:"chat_max_messages"`
	ChatMaxBytes      int64  `json:"chat_max_bytes"`
	ChatMaxAgeSeconds int64  `json:"chat_max_age_seconds"`
}

func validSettings(v ServerSettings) bool {
	return validName(v.Name, 100) && v.MaxClients >= 1 && v.MaxClients <= 64 && v.ChatMaxMessages >= 1 && v.ChatMaxMessages <= 10000 && v.ChatMaxBytes >= 16<<10 && v.ChatMaxBytes <= 4<<30 && v.ChatMaxAgeSeconds >= 1 && v.ChatMaxAgeSeconds <= 365*24*3600
}

func (s *Store) initializeSettings(name string, maxClients int) error {
	if s.snapshot().Settings != nil {
		return nil
	}
	cfg := s.chat.stream.CachedInfo().Config
	v := ServerSettings{Name: name, MaxClients: maxClients, ChatMaxMessages: cfg.MaxMsgsPerSubject, ChatMaxBytes: cfg.MaxBytes, ChatMaxAgeSeconds: int64(cfg.MaxAge / time.Second)}
	if !validSettings(v) {
		return ErrInvalid
	}
	return s.update(func(r *resourceRecord) error {
		if r.Settings == nil {
			r.Settings = &v
		}
		return nil
	})
}

func (c *chatStore) configureRetention(ctx context.Context, v ServerSettings) error {
	c.retentionMu.Lock()
	defer c.retentionMu.Unlock()
	cfg := c.stream.CachedInfo().Config
	age := time.Duration(v.ChatMaxAgeSeconds) * time.Second
	if cfg.MaxMsgsPerSubject == v.ChatMaxMessages && cfg.MaxBytes == v.ChatMaxBytes && cfg.MaxAge == age {
		return nil
	}
	cfg.MaxMsgsPerSubject, cfg.MaxBytes, cfg.MaxAge = v.ChatMaxMessages, v.ChatMaxBytes, age
	cfg.Duplicates = min(2*time.Minute, age)
	_, err := c.js.UpdateStream(ctx, cfg)
	if err != nil {
		return err
	}
	// Keep the stream handle stable: concurrent chat readers own it. Refresh its
	// metadata rather than replacing a pointer used by voice-adjacent workers.
	_, err = c.stream.Info(ctx)
	return err
}

func (s *Server) SetSettings(ctx context.Context, actor string, v ServerSettings, revision uint64) error {
	v.Name = strings.TrimSpace(v.Name)
	if !validSettings(v) {
		return ErrInvalid
	}
	s.channelMu.Lock()
	defer s.channelMu.Unlock()
	defer func() { s.mu.Lock(); s.pendingMaxClients = 0; s.mu.Unlock() }()
	err := s.config.Store.updateAtContext(ctx, revision, func(r *resourceRecord) error {
		if !authorized(*r, actor, "settings", "write", "*") {
			return ErrPermission
		}
		s.mu.Lock()
		count := len(s.peers)
		if v.MaxClients >= count {
			s.pendingMaxClients = v.MaxClients
		}
		s.mu.Unlock()
		if v.MaxClients < count {
			return ErrInvalid
		}
		r.Settings = &v
		return nil
	})
	if err != nil {
		return err
	}
	applyCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err = s.chat.configureRetention(applyCtx, v); err != nil {
		return err
	}
	s.mu.Lock()
	s.config.Name = v.Name
	s.config.MaxClients = v.MaxClients
	s.broadcastLocked(nil)
	s.mu.Unlock()
	return nil
}

func (a *AdminConsole) settings(wr http.ResponseWriter, r *http.Request) {
	session, ok := a.authenticate(wr, r)
	if !ok {
		return
	}
	var v struct {
		Revision uint64         `json:"revision"`
		Settings ServerSettings `json:"settings"`
	}
	if !adminDecode(wr, r, &v) {
		return
	}
	if v.Revision == 0 {
		adminFailure(wr, ErrInvalid)
		return
	}
	if err := a.server.SetSettings(r.Context(), session.Identity, v.Settings, v.Revision); err != nil {
		adminFailure(wr, err)
		return
	}
	adminJSON(wr, 200, map[string]bool{"ok": true})
}
