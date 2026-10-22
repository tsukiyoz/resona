package server

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	w "github.com/tsukiyoz/resona/internal/nativewire"
)

func TestServerSettingsAuthorizePersistAndDoNotApplyStartupLimitsFirst(t *testing.T) {
	s, owner, dir := channelTestServer(t)
	s.chat = s.config.Store.chat
	ctx := context.Background()
	v := ServerSettings{Name: "Managed", MaxClients: 8, ChatMaxMessages: 300, ChatMaxBytes: 64 << 20, ChatMaxAgeSeconds: 7 * 24 * 3600}
	_, rev := s.config.Store.state()
	if err := s.SetSettings(ctx, strings.Repeat("b", 64), v, rev); !errors.Is(err, ErrPermission) {
		t.Fatal(err)
	}
	if err := s.SetSettings(ctx, owner.identity, v, rev); err != nil {
		t.Fatal(err)
	}
	if s.config.Name != v.Name || s.config.MaxClients != 8 {
		t.Fatal("settings not applied")
	}
	if err := s.SetSettings(ctx, owner.identity, v, rev); !errors.Is(err, ErrConflict) {
		t.Fatal("stale settings accepted", err)
	}
	for i := range 3 {
		publishChat(t, s.chat, 1, i, "retained")
	}
	s.config.Store.Close()
	reopened, err := OpenStore(ChatConfig{Directory: filepath.Join(dir, "chat"), MaxMessagesPerChannel: 1}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	info, err := reopened.chat.stream.Info(ctx)
	if err != nil || info.State.Msgs != 3 || info.Config.MaxMsgsPerSubject != 300 {
		t.Fatal("startup flags evicted saved history before reading KV", err)
	}
	if got := reopened.snapshot().Settings; got == nil || *got != v {
		t.Fatal("settings lost on restart")
	}
	// No open listener is needed to validate the active member capacity guard.
	s.config.Store = reopened
	s.chat = reopened.chat
	s.peers[1] = &peer{member: w.Member{Channel: 1}, out: make(chan []byte, 8)}
	s.peers[2] = &peer{member: w.Member{Channel: 1}, out: make(chan []byte, 8)}
	v.MaxClients = 1
	_, rev = reopened.state()
	if err := s.SetSettings(ctx, owner.identity, v, rev); !errors.Is(err, ErrInvalid) {
		t.Fatal("capacity reduced below occupancy", err)
	}
	if s.pendingMaxClients != 0 {
		t.Fatal("pending admission limit leaked")
	}
	delete(s.peers, 1)
	delete(s.peers, 2)
	v.MaxClients = 8
	v.ChatMaxMessages = 1
	if err := s.SetSettings(ctx, owner.identity, v, rev); err != nil {
		t.Fatal(err)
	}
	info, _ = s.chat.stream.Info(ctx)
	if info.State.Msgs != 1 {
		t.Fatal("new retention not applied")
	}
}
