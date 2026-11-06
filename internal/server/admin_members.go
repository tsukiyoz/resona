package server

import (
	"context"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"time"

	w "github.com/tsukiyoz/resona/internal/nativewire"
)

type Ban struct {
	Identity string    `json:"identity"`
	Reason   string    `json:"reason"`
	Expires  time.Time `json:"expires"`
}

func isBanned(r resourceRecord, identity string) bool {
	return slices.ContainsFunc(r.Bans, func(b Ban) bool { return b.Identity == identity && time.Now().Before(b.Expires) })
}

func (s *Store) Banned(identity string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return isBanned(s.record, identity)
}

func (s *Server) SetBan(ctx context.Context, actor, identity, reason string, ttl time.Duration, remove bool, revision uint64) error {
	s.channelMu.Lock()
	defer s.channelMu.Unlock()
	err := s.config.Store.updateAtContext(ctx, revision, func(r *resourceRecord) error {
		if identity == r.Owner || !authorized(*r, actor, "bans", "write", roleOf(*r, identity)) {
			return ErrPermission
		}
		if !validIdentity(identity) || (!remove && (ttl < time.Minute || ttl > 365*24*time.Hour || (reason != "" && !validName(reason, 200)))) {
			return ErrInvalid
		}
		r.Bans = slices.DeleteFunc(r.Bans, func(b Ban) bool { return b.Identity == identity || !time.Now().Before(b.Expires) })
		if !remove {
			if len(r.Bans) >= 256 {
				return ErrInvalid
			}
			r.Bans = append(r.Bans, Ban{Identity: identity, Reason: reason, Expires: time.Now().UTC().Add(ttl)})
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.disconnectBanned()
	slog.Info("management updated ban", "actor", actor, "identity", identity, "removed", remove)
	return nil
}

func (s *Server) disconnectBanned() {
	s.mu.Lock()
	var closing []*peer
	for _, p := range s.peers {
		if (s.config.Store.Banned(p.identity) || s.config.Store.Banned(p.principal())) && !p.removed {
			p.removed = true
			p.member.Muted = true
			p.member.Deafened = true
			closing = append(closing, p)
		}
	}
	if len(closing) > 0 {
		s.broadcastLocked(nil)
	} else {
		s.signalAdminLocked()
	}
	s.mu.Unlock()
	for _, p := range closing {
		if p.voiceOut != nil {
			p.voiceOut.Close()
		}
		_ = p.conn.CloseWithError(w.MemberBanned, "membership revoked")
	}
}

// Live actions target a particular session, never just a recyclable UI row.
func (s *Server) ModerateMember(ctx context.Context, actor string, id uint16, instance, action string, channel uint16, revision uint64) error {
	return s.moderateMember(ctx, actor, nil, id, instance, action, channel, revision)
}

func (s *Server) moderateMember(ctx context.Context, actor string, actorPeer *peer, id uint16, instance, action string, channel uint16, revision uint64) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	s.channelMu.Lock()
	defer s.channelMu.Unlock()
	s.mu.Lock()
	r, current := s.config.Store.state()
	if revision != 0 && revision != current {
		s.mu.Unlock()
		return ErrConflict
	}
	if actorPeer != nil {
		if s.peers[actorPeer.member.ID] != actorPeer || actorPeer.removed || (actorPeer.account.UID != "" && !time.Now().Before(actorPeer.accountExpires)) {
			s.mu.Unlock()
			return ErrPermission
		}
		actor = actorPeer.principal()
	}
	p := s.peers[id]
	if p == nil || p.removed || p.member.Instance != instance {
		s.mu.Unlock()
		return ErrNotFound
	}
	if !authorized(r, actor, "members", "write", roleOf(r, p.principal())) {
		s.mu.Unlock()
		return ErrPermission
	}
	switch action {
	case "kick":
		p.removed = true
		p.member.Muted = true
		p.member.Deafened = true
	case "move":
		ch, found := channelByID(r, channel)
		if !found {
			s.mu.Unlock()
			return ErrInvalid
		}
		temporary := ch.AdminOnly && roleOf(r, p.principal()) != "admin" && roleOf(r, p.principal()) != "owner"
		if temporary {
			present := false
			for _, candidate := range s.peers {
				if !candidate.removed && candidate.principal() == actor && candidate.member.Channel == channel && time.Now().Before(candidate.accountExpires) {
					present = true
					break
				}
			}
			if !present {
				s.mu.Unlock()
				return ErrPermission
			}
		}
		if !s.movePeerLocked(p, channel, temporary) {
			s.mu.Unlock()
			return ErrInvalid
		}
	default:
		s.mu.Unlock()
		return ErrInvalid
	}
	s.broadcastLocked(nil)
	s.mu.Unlock()
	if action == "kick" {
		if p.voiceOut != nil {
			p.voiceOut.Close()
		}
		_ = p.conn.CloseWithError(w.MemberKicked, "removed by administrator")
	}
	slog.Info("management changed member", "actor", actor, "member_id", id, "session", instance, "action", action)
	return nil
}

func (a *AdminConsole) registerMembers() {
	a.mux.HandleFunc("PUT "+adminBase+"/roles/{identity}", a.role)
	a.mux.HandleFunc("POST "+adminBase+"/members/{id}/{action}", a.member)
	a.mux.HandleFunc("POST "+adminBase+"/bans/{identity}", a.ban)
	a.mux.HandleFunc("DELETE "+adminBase+"/bans/{identity}", a.ban)
}

func (a *AdminConsole) role(wr http.ResponseWriter, r *http.Request) {
	session, ok := a.authenticate(wr, r)
	if !ok {
		return
	}
	var v struct {
		Revision uint64 `json:"revision"`
		Role     string `json:"role"`
	}
	if !adminDecode(wr, r, &v) {
		return
	}
	if v.Revision == 0 {
		adminFailure(wr, ErrInvalid)
		return
	}
	if _, err := a.server.config.Store.accounts.Account(r.Context(), r.PathValue("identity")); err != nil {
		adminFailure(wr, ErrNotFound)
		return
	}
	err := a.server.config.Store.SetRole(r.Context(), session.Identity, r.PathValue("identity"), v.Role, v.Revision)
	if err != nil {
		adminFailure(wr, err)
		return
	}
	a.server.notifyAdminChange()
	adminJSON(wr, 200, map[string]bool{"ok": true})
}

func (s *Server) notifyAdminChange() {
	s.mu.Lock()
	s.enforceChannelAccessLocked(s.config.Store.snapshot())
	s.broadcastLocked(nil)
	s.mu.Unlock()
}

func (s *Server) moderationCommand(p *peer, f w.Frame) bool {
	var cmd w.ModerateMember
	if w.Decode(f, &cmd) != nil {
		return false
	}
	s.mu.Lock()
	allowed := !p.removed && p.commandBucket.take(2, 4)
	s.mu.Unlock()
	code := uint8(w.RateLimited)
	if allowed {
		err := s.moderateMember(p.conn.Context(), "", p, cmd.Target, cmd.Instance, cmd.Action, cmd.Channel, 0)
		switch err {
		case nil:
			code = w.OK
		case ErrPermission:
			code = w.PermissionDenied
		case ErrNotFound:
			code = w.MemberUnavailable
		default:
			code = w.Rejected
		}
	}
	s.mu.Lock()
	s.enqueueLocked(p, w.ReplyKind, f.Request, w.Reply{Code: code})
	s.mu.Unlock()
	return true
}

func (a *AdminConsole) member(wr http.ResponseWriter, r *http.Request) {
	session, ok := a.authenticate(wr, r)
	if !ok {
		return
	}
	var v struct {
		Revision uint64 `json:"revision"`
		Instance string `json:"instance"`
		Channel  uint16 `json:"channel"`
	}
	if !adminDecode(wr, r, &v) {
		return
	}
	id, err := strconv.ParseUint(r.PathValue("id"), 10, 16)
	if err != nil || id == 0 || v.Revision == 0 || v.Instance == "" {
		adminFailure(wr, ErrInvalid)
		return
	}
	if err = a.server.ModerateMember(r.Context(), session.Identity, uint16(id), v.Instance, r.PathValue("action"), v.Channel, v.Revision); err != nil {
		adminFailure(wr, err)
		return
	}
	adminJSON(wr, 200, map[string]bool{"ok": true})
}

func (a *AdminConsole) ban(wr http.ResponseWriter, r *http.Request) {
	session, ok := a.authenticate(wr, r)
	if !ok {
		return
	}
	var v struct {
		Revision uint64 `json:"revision"`
		Reason   string `json:"reason"`
		Minutes  int    `json:"minutes"`
	}
	if !adminDecode(wr, r, &v) {
		return
	}
	if v.Revision == 0 || v.Minutes < 0 || v.Minutes > 365*24*60 {
		adminFailure(wr, ErrInvalid)
		return
	}
	if err := a.server.SetBan(r.Context(), session.Identity, r.PathValue("identity"), v.Reason, time.Duration(v.Minutes)*time.Minute, r.Method == "DELETE", v.Revision); err != nil {
		adminFailure(wr, err)
		return
	}
	adminJSON(wr, 200, map[string]bool{"ok": true})
}
