package server

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tsukiyoz/resona/internal/accounts"
	w "github.com/tsukiyoz/resona/internal/nativewire"
)

func accessTestPeer(id uint16, uid string) *peer {
	p := &peer{identity: strings.Repeat("f", 64), conn: &chatTestConnection{ctx: context.Background()}, member: w.Member{ID: id, Instance: "connection", Channel: 1, Epoch: 1}, epoch: 1, out: make(chan []byte, 128), chatWake: make(chan struct{}, 1)}
	if uid != "" {
		p.account = accounts.Proof{Account: accounts.Account{UID: uid}}
		p.accountExpires = time.Now().Add(time.Hour)
	}
	return p
}

func TestRestrictedAdmissionAndTemporaryMove(t *testing.T) {
	s, owner, dir := channelTestServer(t)
	ctx := context.Background()
	adminUID := strings.Repeat("a", 64)
	if err := s.config.Store.SetRole(ctx, owner.principal(), adminUID, "admin", 0); err != nil {
		t.Fatal(err)
	}
	if code := s.manageChannel(owner, w.UpdateChannelKind, 2, "Private", "", ChannelOptions{AdminOnly: true}); code != w.OK {
		t.Fatal(code)
	}
	r := s.config.Store.snapshot()
	ch, _ := channelByID(r, 2)
	admin, guest, member := accessTestPeer(1, adminUID), accessTestPeer(2, ""), accessTestPeer(3, strings.Repeat("b", 64))
	s.peers = map[uint16]*peer{1: admin, 2: guest, 3: member}
	if canEnterChannel(r, guest, ch) || canEnterChannel(r, member, ch) || !canEnterChannel(r, admin, ch) || !canEnterChannel(r, owner, ch) {
		t.Fatal("wrong channel admission")
	}
	_, revision := s.config.Store.state()
	move := func(p *peer) error {
		return s.ModerateMember(ctx, adminUID, p.member.ID, p.member.Instance, "move", 2, revision)
	}
	if err := move(guest); !errors.Is(err, ErrPermission) {
		t.Fatal("off-channel invitation accepted", err)
	}
	s.movePeerLocked(admin, 2, false)
	for _, p := range []*peer{guest, member} {
		p.member.Muted, p.member.Deafened = true, true
		if err := move(p); err != nil {
			t.Fatal(err)
		}
		if p.temporaryChannel != 2 || p.member.Channel != 2 || p.epoch != 2 || !p.member.Muted || !p.member.Deafened || p.chatGeneration != 1 {
			t.Fatal("move changed voice intent or missed scope boundary")
		}
	}
	s.movePeerLocked(admin, 1, false)
	s.enforceChannelAccessLocked(r)
	if guest.member.Channel != 2 {
		t.Fatal("inviter departure revoked current admission")
	}
	s.movePeerLocked(guest, 1, false)
	if guest.temporaryChannel != 0 || canEnterChannel(r, guest, ch) || canEnterChannel(r, accessTestPeer(2, ""), ch) {
		t.Fatal("temporary admission survived departure/reconnect")
	}
	if err := s.ModerateMember(ctx, adminUID, member.member.ID, "stale", "move", 1, revision); !errors.Is(err, ErrNotFound) {
		t.Fatal("stale session accepted", err)
	}
	s.config.Store.Close()
	s.config.Store = testStore(t, dir)
	if !s.config.Store.snapshot().Channels.Channels[1].AdminOnly {
		t.Fatal("channel access lost on restart")
	}
}

func TestAccessChangesEjectWithoutChangingVoiceIntent(t *testing.T) {
	s, owner, _ := channelTestServer(t)
	uid := strings.Repeat("a", 64)
	ctx := context.Background()
	if err := s.config.Store.SetRole(ctx, owner.principal(), uid, "admin", 0); err != nil {
		t.Fatal(err)
	}
	p := accessTestPeer(1, uid)
	p.member.Channel, p.member.Muted, p.member.Deafened = 2, true, true
	s.peers[1] = p
	if code := s.manageChannel(owner, w.UpdateChannelKind, 2, "Private", "", ChannelOptions{AdminOnly: true}); code != w.OK {
		t.Fatal(code)
	}
	if p.member.Channel != 2 {
		t.Fatal("administrator ejected")
	}
	if err := s.config.Store.SetRole(ctx, owner.principal(), uid, "member", 0); err != nil {
		t.Fatal(err)
	}
	s.notifyAdminChange()
	if p.member.Channel != 1 || p.epoch != 2 || p.chatGeneration != 1 || !p.member.Muted || !p.member.Deafened {
		t.Fatal("role demotion missed route boundary")
	}
	if code := s.manageChannel(owner, w.UpdateChannelKind, 1, "Lobby", "", ChannelOptions{AdminOnly: true}); code != w.DefaultChannel {
		t.Fatal("default channel restricted", code)
	}
	p.member.Channel = 2
	if code := s.manageChannel(owner, w.UpdateChannelKind, 2, "Public", "", ChannelOptions{}); code != w.OK {
		t.Fatal(code)
	}
	if code := s.manageChannel(owner, w.UpdateChannelKind, 2, "Private", "", ChannelOptions{AdminOnly: true}); code != w.OK || p.member.Channel != 1 {
		t.Fatal("tightened access left member inside", code)
	}
}

func TestOnlyOwnerCanBanAndAdminsCannotKickPeers(t *testing.T) {
	s, owner, _ := channelTestServer(t)
	ctx := context.Background()
	adminUID, otherUID := strings.Repeat("a", 64), strings.Repeat("b", 64)
	for _, uid := range []string{adminUID, otherUID} {
		if err := s.config.Store.SetRole(ctx, owner.principal(), uid, "admin", 0); err != nil {
			t.Fatal(err)
		}
	}
	for _, remove := range []bool{false, true} {
		if err := s.SetBan(ctx, adminUID, otherUID, "", time.Hour, remove, 0); !errors.Is(err, ErrPermission) {
			t.Fatal("admin changed ban", err)
		}
	}
	p := accessTestPeer(1, otherUID)
	s.peers[1] = p
	if err := s.ModerateMember(ctx, adminUID, 1, p.member.Instance, "kick", 0, 0); !errors.Is(err, ErrPermission) {
		t.Fatal("admin kicked peer", err)
	}
	if err := s.ModerateMember(ctx, owner.principal(), 1, p.member.Instance, "kick", 0, 0); err != nil || !p.removed {
		t.Fatal("owner failed to kick admin", err)
	}
}
