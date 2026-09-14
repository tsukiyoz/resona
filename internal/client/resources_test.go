package client

import "testing"

func TestScopedResourcesPreservePlaybackWithoutReusingAnIdentity(t *testing.T) {
	s := &Service{state: Workspace{Session: Session{ID: "live", Protocol: "resona-noise"}, Users: []User{{ID: "2", Instance: "old", PlaybackVolume: 150, PlaybackMuted: true}}}}
	s.state.Users = s.reconcileScopedUserPlayback(nil, true)
	if len(s.state.Users) != 0 {
		t.Fatal("hidden members retained as visible")
	}
	s.state.Users = s.reconcileScopedUserPlayback([]User{{ID: "2", Instance: "old"}}, false)
	if s.state.Users[0].PlaybackVolume != 150 || !s.state.Users[0].PlaybackMuted {
		t.Fatal("scope change lost playback preference")
	}
	s.state.Users = s.reconcileScopedUserPlayback([]User{{ID: "2", Instance: "new"}}, false)
	if s.state.Users[0].PlaybackVolume != 100 || s.state.Users[0].PlaybackMuted || len(s.playbackCache) != 0 {
		t.Fatal("reused ID inherited playback preference")
	}
}

func TestNotificationUpdatesAreLightweightBoundedAndIndependent(t *testing.T) {
	s := &Service{state: Workspace{Session: Session{ID: "live"}, Messages: []Message{{Text: "private chat"}}}}
	if s.GetNotificationUpdate("") != nil {
		t.Fatal("empty notification event")
	}
	s.addNotificationLocked("member_joined", "1")
	update := s.GetNotificationUpdate("")
	if update == nil || update.Session.ID != "live" || len(update.Notifications) != 1 {
		t.Fatal("missing live notification")
	}
	id := update.Notifications[0].ID
	if s.GetNotificationUpdate(id) != nil {
		t.Fatal("duplicate notification update")
	}
	update.Notifications[0].Kind = "changed"
	if s.state.Notifications[0].Kind != "member_joined" {
		t.Fatal("mutable notification alias")
	}
	for range 100 {
		s.addNotificationLocked("member_left", "1")
	}
	if got := s.GetNotificationUpdate(id); got == nil || len(got.Notifications) != maxNotifications {
		t.Fatal("notification history unbounded or missing")
	}
}
