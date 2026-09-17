package client

import (
	"testing"

	"github.com/tsukiyoz/resona/internal/audio"
)

type playbackEngine struct {
	fakeVoiceEngine
	peers map[uint16]audio.PeerPlayback
}

func (e *playbackEngine) SetPeerPlayback(peers map[uint16]audio.PeerPlayback) { e.peers = peers }

func TestUserPlaybackValidatesTargetAndDoesNotReopenDevices(t *testing.T) {
	e := &playbackEngine{}
	s := &Service{connection: &voiceConnection{}, voice: e, state: Workspace{
		Session: Session{ID: "session", Mode: "connected", ChannelID: "10"},
		Users:   []User{{ID: "1", Instance: "self", Self: true, ChannelID: "10", PlaybackVolume: 100}, {ID: "2", Instance: "alice", ChannelID: "10", PlaybackVolume: 100}, {ID: "3", Instance: "bob", ChannelID: "10", PlaybackVolume: 100}},
	}}
	for _, volume := range []int{0, 200, 50} {
		if _, err := s.SetUserPlayback("session", "2", "alice", volume, true); err != nil {
			t.Fatal(err)
		}
	}
	if e.peers[2].Volume != 50 || !e.peers[2].Muted || e.peers[3].Volume != 100 || e.peers[3].Muted {
		t.Fatal("per-user control changed wrong peer")
	}
	if _, err := s.SetUserPlayback("session", "2", "alice", 50, false); err != nil {
		t.Fatal(err)
	}
	if e.peers[2].Muted || e.peers[2].Volume != 50 {
		t.Fatal("unmute lost volume")
	}
	for _, tc := range []struct {
		session, id, instance string
		volume                int
	}{
		{"old", "2", "alice", 100}, {"session", "2", "old-alice", 100}, {"session", "1", "self", 100}, {"session", "2", "alice", 795}, {"session", "2", "alice", -1},
	} {
		if _, err := s.SetUserPlayback(tc.session, tc.id, tc.instance, tc.volume, true); err == nil {
			t.Fatal("stale/invalid target accepted")
		}
	}
	if e.state.Active || e.changed.Load() != 0 {
		t.Fatal("local gain touched device lifecycle")
	}
	s.state.Users[1].ChannelID = "other"
	if _, err := s.SetUserPlayback("session", "2", "alice", 100, true); err == nil {
		t.Fatal("different channel accepted")
	}
}

func TestUserPlaybackFollowsInstanceNotNameOrReusedID(t *testing.T) {
	old := []User{{ID: "2", Instance: "one", Nickname: "same", ChannelID: "10", PlaybackVolume: 150, PlaybackMuted: true}}
	next := []User{{ID: "2", Instance: "one", Nickname: "renamed", ChannelID: "20"}}
	users := reconcileUserPlayback(old, next)
	if users[0].PlaybackVolume != 150 || !users[0].PlaybackMuted {
		t.Fatal("same member lost settings after move/rename")
	}
	next[0].Instance, next[0].Nickname = "two", "same"
	users = reconcileUserPlayback(old, next)
	if users[0].PlaybackVolume != 100 || users[0].PlaybackMuted {
		t.Fatal("new member inherited old user's settings")
	}
	users = reconcileUserPlayback(nil, old)
	if users[0].PlaybackVolume != 100 || users[0].PlaybackMuted {
		t.Fatal("new session accepted remote local-playback values")
	}
}
