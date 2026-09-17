package server

import (
	"os"
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
	token, err := InitOwnerClaim(dir)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := OpenOwnership(dir)
	if err != nil {
		t.Fatal(err)
	}
	p := &peer{identity: strings.Repeat("a", 64)}
	if err = owner.Claim(p.identity, token); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "channels.json")
	store, err := OpenChannelStore(path, []w.Channel{{ID: 1, Name: "Lobby"}, {ID: 2, Name: "Gaming"}})
	if err != nil {
		t.Fatal(err)
	}
	return &Server{config: Config{Ownership: owner, ChannelStore: store, Channels: store.record.Channels}, peers: map[uint16]*peer{}}, p, path
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
	reloaded, err := OpenChannelStore(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s.config.Channels, reloaded.record.Channels) || reloaded.record.Next != 4 {
		t.Fatal("restart mismatch")
	}
	s.config.ChannelStore = reloaded
	if code := s.manageChannel(owner, w.CreateChannelKind, 0, "Later", ""); code != w.OK {
		t.Fatal(code)
	}
	if s.config.Channels[2].ID != 4 {
		t.Fatal("ID reused")
	}
}

func TestChannelsFailedStorageAndValidationPreserveState(t *testing.T) {
	s, owner, path := channelTestServer(t)
	before := append([]w.Channel(nil), s.config.Channels...)
	for _, name := range []string{" ", "bad\nname", strings.Repeat("x", 101)} {
		if code := s.manageChannel(owner, w.CreateChannelKind, 0, name, ""); code != w.Rejected {
			t.Fatal(code)
		}
	}
	if code := s.manageChannel(owner, w.UpdateChannelKind, 2, "valid", strings.Repeat("x", 1025)); code != w.Rejected {
		t.Fatal(code)
	}
	if err := os.Rename(path, path+".backup"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if code := s.manageChannel(owner, w.DeleteChannelKind, 2, "", ""); code != w.StorageFailed {
		t.Fatal(code)
	}
	if !reflect.DeepEqual(before, s.config.Channels) || s.deletingChannel != 0 || s.config.ChannelStore.record.Next != 3 {
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
	reloaded, err := OpenChannelStore(path, nil)
	if err != nil || !reflect.DeepEqual(before, reloaded.record.Channels) {
		t.Fatalf("restart lost audio: %v", err)
	}
}
