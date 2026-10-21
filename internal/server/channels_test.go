package server

import (
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	w "github.com/tsukiyoz/resona/internal/nativewire"
)

func channelTestServer(t *testing.T) (*Server, *peer, string) {
	t.Helper()
	dir := t.TempDir()
	store := testStore(t, dir)
	token, err := store.ProvisionOwnerClaim(false)
	if err != nil {
		t.Fatal(err)
	}
	p := &peer{identity: strings.Repeat("a", 64)}
	if err = store.Claim(p.identity, token); err != nil {
		t.Fatal(err)
	}
	return &Server{config: Config{Store: store, Channels: store.snapshot().Channels.Channels}, peers: map[uint16]*peer{}}, p, dir
}

func TestChannelsPermissionPersistenceAndStableIDs(t *testing.T) {
	s, owner, path := channelTestServer(t)
	if code := s.manageChannel(&peer{identity: strings.Repeat("b", 64)}, w.CreateChannelKind, 0, "forbidden", ""); code != w.PermissionDenied {
		t.Fatal(code)
	}
	if code := s.manageChannel(owner, w.DeleteChannelKind, 1, "", ""); code != w.DefaultChannel {
		t.Fatal(code)
	}
	s.peers[9] = &peer{member: w.Member{Channel: 2}}
	if code := s.manageChannel(owner, w.DeleteChannelKind, 2, "", ""); code != w.ChannelNotEmpty {
		t.Fatal(code)
	}
	delete(s.peers, 9)
	if code := s.manageChannel(owner, w.CreateChannelKind, 0, " New ", "line1\nline2"); code != w.OK {
		t.Fatal(code)
	}
	if code := s.manageChannel(owner, w.UpdateChannelKind, 3, "Renamed", "changed"); code != w.OK {
		t.Fatal(code)
	}
	if code := s.manageChannel(owner, w.DeleteChannelKind, 2, "", ""); code != w.OK {
		t.Fatal(code)
	}
	s.config.Store.Close()
	reloaded := testStore(t, path)
	if !reflect.DeepEqual(s.config.Channels, reloaded.snapshot().Channels.Channels) || reloaded.snapshot().Channels.Next != 4 {
		t.Fatal("restart mismatch")
	}
	s.config.Store = reloaded
	if code := s.manageChannel(owner, w.CreateChannelKind, 0, "Later", ""); code != w.OK {
		t.Fatal(code)
	}
	if s.config.Channels[2].ID != 4 {
		t.Fatal("ID reused")
	}
}

func TestChannelsFailedStorageAndValidationPreserveState(t *testing.T) {
	s, owner, _ := channelTestServer(t)
	before := append([]w.Channel(nil), s.config.Channels...)
	for _, name := range []string{" ", "bad\nname", strings.Repeat("x", 101)} {
		if code := s.manageChannel(owner, w.CreateChannelKind, 0, name, ""); code != w.Rejected {
			t.Fatal(code)
		}
	}
	if code := s.manageChannel(owner, w.UpdateChannelKind, 2, "valid", strings.Repeat("x", 1025)); code != w.Rejected {
		t.Fatal(code)
	}
	s.config.Store.chat.conn.Close()
	if code := s.manageChannel(owner, w.DeleteChannelKind, 2, "", ""); code != w.StorageFailed {
		t.Fatal(code)
	}
	if !reflect.DeepEqual(before, s.config.Channels) || s.deletingChannel != 0 || s.config.Store.snapshot().Channels.Next != 3 {
		t.Fatal("failed operation changed state")
	}
}

func TestChannelsConcurrentCreatesUniqueAndBounded(t *testing.T) {
	s, owner, _ := channelTestServer(t)
	var wg sync.WaitGroup
	for range 70 {
		wg.Go(func() {
			code := s.manageChannel(owner, w.CreateChannelKind, 0, "channel", "")
			if code != w.OK && code != w.ChannelLimit {
				t.Errorf("code=%d", code)
			}
		})
	}
	wg.Wait()
	if len(s.config.Channels) != w.MaxChannels || !validChannels(s.config.Channels) {
		t.Fatal("channel limit or ID invariant broken")
	}
}

func TestChannelAudioPersistenceValidationAndLegacyEdits(t *testing.T) {
	s, owner, path := channelTestServer(t)
	if w.ChannelBitrate(s.config.Channels[0].Bitrate) != 48000 {
		t.Fatal("legacy seed changed")
	}
	if code := s.manageChannel(owner, w.CreateChannelKind, 0, "default", ""); code != w.OK || s.config.Channels[2].Bitrate != 32000 {
		t.Fatal("wrong new channel default")
	}
	for _, bitrate := range []uint32{16000, 20000, 32000, 48000, 64000, 37000} {
		if code := s.manageChannel(owner, w.UpdateChannelKind, 2, "Gaming", "", bitrate); code != w.OK {
			t.Fatal(code)
		}
	}
	if code := s.manageChannel(owner, w.UpdateChannelKind, 2, "Legacy rename", "", 0); code != w.OK || s.config.Channels[1].Bitrate != 37000 {
		t.Fatal("old client reset bitrate")
	}
	before := append([]w.Channel(nil), s.config.Channels...)
	for _, bitrate := range []uint32{1, 15999, 16500, 64001, ^uint32(0)} {
		if code := s.manageChannel(owner, w.UpdateChannelKind, 2, "invalid", "", bitrate); code != w.Rejected {
			t.Fatal(code)
		}
	}
	if !reflect.DeepEqual(before, s.config.Channels) {
		t.Fatal("rejection changed metadata")
	}
	s.config.Store.Close()
	reloaded := testStore(t, path)
	if !reflect.DeepEqual(before, reloaded.snapshot().Channels.Channels) {
		t.Fatal("restart lost audio")
	}
}

func testStore(t *testing.T, dir string) *Store {
	t.Helper()
	s, err := OpenStore(ChatConfig{Directory: filepath.Join(dir, "chat")}, dir, []w.Channel{{ID: 1, Name: "Lobby"}, {ID: 2, Name: "Gaming"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}
