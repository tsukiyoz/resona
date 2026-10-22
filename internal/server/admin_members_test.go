package server

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	w "github.com/tsukiyoz/resona/internal/nativewire"
)

func TestModerationChecksTargetSessionAndRole(t *testing.T) {
	s, owner, _ := channelTestServer(t)
	ctx := context.Background()
	admin, member := strings.Repeat("b", 64), strings.Repeat("c", 64)
	if err := s.config.Store.SetRole(ctx, owner.identity, admin, "admin", 0); err != nil {
		t.Fatal(err)
	}
	conn := &chatTestConnection{ctx: ctx}
	p := &peer{identity: member, conn: conn, member: w.Member{ID: 1, Instance: "session-1", Channel: 1, Epoch: 1, Nickname: "Member"}, epoch: 1, out: make(chan []byte, 16), chatWake: make(chan struct{}, 1)}
	s.peers[1] = p
	_, rev := s.config.Store.state()
	if err := s.ModerateMember(ctx, admin, 1, "old-session", "kick", 0, rev); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if conn.closed.Load() {
		t.Fatal("wrong session closed")
	}
	if err := s.ModerateMember(ctx, admin, 1, p.member.Instance, "move", 2, rev); err != nil {
		t.Fatal(err)
	}
	if p.member.Channel != 2 || p.epoch != 2 || p.member.Muted {
		t.Fatal("move corrupted route/voice state")
	}
	p.identity = owner.identity
	if err := s.ModerateMember(ctx, admin, 1, p.member.Instance, "kick", 0, rev); !errors.Is(err, ErrPermission) {
		t.Fatal("admin moderated owner", err)
	}
	p.identity = member
	if err := s.ModerateMember(ctx, admin, 1, p.member.Instance, "kick", 0, rev); err != nil {
		t.Fatal(err)
	}
	if !conn.closed.Load() || !p.removed || len(s.adminSnapshot(owner.identity).Members) != 0 {
		t.Fatal("kick left an active route")
	}
}

func TestBanPersistsDisconnectsAndExpiresWithoutTimers(t *testing.T) {
	s, owner, dir := channelTestServer(t)
	ctx := context.Background()
	member := strings.Repeat("b", 64)
	conn := &chatTestConnection{ctx: ctx}
	s.peers[1] = &peer{identity: member, conn: conn, member: w.Member{ID: 1, Instance: "session-1", Channel: 1}, out: make(chan []byte, 8)}
	_, rev := s.config.Store.state()
	if err := s.SetBan(ctx, owner.identity, owner.identity, "", time.Hour, false, rev); !errors.Is(err, ErrPermission) {
		t.Fatal("owner banned", err)
	}
	if err := s.SetBan(ctx, owner.identity, member, "Spam", time.Hour, false, rev); err != nil {
		t.Fatal(err)
	}
	if !conn.closed.Load() || !s.config.Store.Banned(member) {
		t.Fatal("ban did not close live session")
	}
	s.config.Store.Close()
	s.config.Store = testStore(t, dir)
	if !s.config.Store.Banned(member) {
		t.Fatal("ban lost on restart")
	}
	if err := s.config.Store.update(func(r *resourceRecord) error { r.Bans[0].Expires = time.Now().Add(-time.Second); return nil }); err != nil {
		t.Fatal(err)
	}
	if s.config.Store.Banned(member) {
		t.Fatal("expired ban enforced")
	}
}
