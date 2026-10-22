package server

import (
	"context"
	"errors"
	"time"

	"github.com/tsukiyoz/resona/internal/accounts"
	w "github.com/tsukiyoz/resona/internal/nativewire"
)

func (p *peer) principal() string { return p.account.UID }

// ResetAccountPassword is an offline recovery operation under LockAccess.
func (s *Store) ResetAccountPassword(ctx context.Context, name, password string) (string, error) {
	a, err := s.accounts.ResetPassword(ctx, name, password)
	return a.UID, err
}

func (p *peer) authorUID() string {
	if p.account.UID != "" {
		return p.account.UID
	}
	return p.identity
}

func accountCode(err error) uint8 {
	switch {
	case err == nil:
		return w.OK
	case errors.Is(err, accounts.ErrCredentials):
		return w.AccountInvalid
	case errors.Is(err, accounts.ErrExists):
		return w.AccountNameTaken
	case errors.Is(err, accounts.ErrInvalid):
		return w.AccountInvalid
	case errors.Is(err, accounts.ErrBusy):
		return w.AccountBusy
	default:
		return w.StorageFailed
	}
}

// Account commands are low-frequency control work. Admission and invalidation
// share one gate, while password hashing/SQL never hold the voice routing lock.
func (s *Server) accountCommand(p *peer, f w.Frame) bool {
	var cmd w.AccountCommand
	if w.Decode(f, &cmd) != nil {
		return false
	}
	s.mu.Lock()
	allowed := !p.removed && p.accountBucket.take(1, 3) && s.accountBucket.take(2, 4)
	s.mu.Unlock()
	if !allowed {
		s.accountReply(p, f.Request, w.RateLimited, "")
		return true
	}
	select {
	case s.accountGate <- struct{}{}:
		defer func() { <-s.accountGate }()
	default:
		s.accountReply(p, f.Request, w.AccountBusy, "")
		return true
	}
	ctx, cancel := context.WithTimeout(p.conn.Context(), 5*time.Second)
	defer cancel()
	s.mu.Lock()
	previous := p.account
	nickname := p.member.Nickname
	active := !p.removed
	s.mu.Unlock()
	if !active {
		return false
	}
	store := s.config.Store.accounts
	var a accounts.Account
	var proof accounts.Proof
	var secret string
	var err error
	switch cmd.Action {
	case "register":
		if previous.UID != "" {
			err = accounts.ErrInvalid
			break
		}
		if cmd.Nickname != "" {
			nickname = cmd.Nickname
		}
		a, err = store.Register(ctx, cmd.Username, cmd.Password, nickname)
	case "password":
		a, err = store.Password(ctx, cmd.Username, cmd.Password)
	case "token":
		proof, err = store.Token(ctx, cmd.Token)
		a = proof.Account
	case "change_password":
		if previous.UID == "" {
			err = accounts.ErrCredentials
			break
		}
		a, err = store.ChangePassword(ctx, previous.Account, cmd.Password, cmd.NewPassword)
	case "update_profile":
		if previous.UID == "" {
			err = accounts.ErrCredentials
			break
		}
		a, err = store.UpdateProfile(ctx, previous.Account, cmd.Nickname)
		if err == nil {
			s.mu.Lock()
			p.account.Account = a
			p.member.Nickname = a.Nickname
			s.broadcastLocked(nil)
			s.mu.Unlock()
		}
		s.accountReply(p, f.Request, accountCode(err), "")
		return true
	case "logout":
		if previous.UID != "" {
			err = store.Revoke(ctx, previous.UID, previous.TokenID)
		}
		if err == nil {
			s.mu.Lock()
			p.account = accounts.Proof{}
			p.accountExpires = time.Time{}
			p.member.Muted = true
			p.epoch++
			p.member.Epoch = p.epoch
			s.restartChatLocked(p)
			s.broadcastLocked(nil)
			s.mu.Unlock()
		}
		s.accountReply(p, f.Request, accountCode(err), "")
		return true
	default:
		err = accounts.ErrInvalid
	}
	if err != nil {
		if cmd.Action == "change_password" && !store.Valid(ctx, previous) {
			_ = p.conn.CloseWithError(w.AccountRevoked, "account credentials changed")
			return false
		}
		s.accountReply(p, f.Request, accountCode(err), "")
		return true
	}
	if s.config.Store.Banned(a.UID) || s.config.Store.Banned(p.identity) {
		s.accountReply(p, f.Request, w.PermissionDenied, "")
		return true
	}
	// Rotate recovery credentials atomically with issuance. Old devices that
	// miss the terminal UDP close cannot reconnect and displace the new login.
	proof, secret, err = store.Issue(ctx, a)
	if err != nil {
		if cmd.Action == "change_password" {
			_ = p.conn.CloseWithError(w.AccountRevoked, "account credentials changed")
			return false
		}
		s.accountReply(p, f.Request, accountCode(err), "")
		return true
	}
	s.mu.Lock()
	if p.removed || p.conn.Context().Err() != nil {
		s.mu.Unlock()
		return false
	}
	var replaced []*peer
	for _, other := range s.peers {
		if other != p && !other.removed && other.principal() == proof.UID {
			other.removed = true
			other.member.Muted = true
			other.member.Deafened = true
			if other.chatCancel != nil {
				other.chatCancel()
			}
			replaced = append(replaced, other)
		}
	}
	p.account = proof
	p.accountExpires = proof.Expires
	p.member.Nickname = proof.Nickname
	p.epoch++
	p.member.Epoch = p.epoch
	s.restartChatLocked(p)
	s.broadcastLocked(nil)
	s.enqueueLocked(p, w.ReplyKind, f.Request, w.Reply{Code: w.OK, AccountToken: secret})
	s.mu.Unlock()
	for _, old := range replaced {
		if old.voiceOut != nil {
			old.voiceOut.Close()
		}
		_ = old.conn.CloseWithError(w.AccountReplaced, "account connected elsewhere")
	}
	return true
}

func (s *Server) accountReply(p *peer, request uint32, code uint8, token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !p.removed {
		s.enqueueLocked(p, w.ReplyKind, request, w.Reply{Code: code, AccountToken: token})
	}
}
