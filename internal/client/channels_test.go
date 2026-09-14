package client

import (
	"context"
	"testing"
)

type channelConnection struct {
	fakeConnection
	mutate func() error
}

func (c *channelConnection) CreateChannel(context.Context, string, string) error { return c.mutate() }
func (c *channelConnection) UpdateChannel(context.Context, string, string, string) error {
	return c.mutate()
}
func (c *channelConnection) DeleteChannel(context.Context, string) error { return c.mutate() }

func TestChannelServiceGuardsAndStaleCompletion(t *testing.T) {
	called := 0
	c := &channelConnection{mutate: func() error { called++; return nil }}
	s := &Service{connection: c, state: Workspace{Session: Session{ID: "current", Mode: "connected", CanManageChannels: true}, Channels: []Channel{{ID: "1", IsDefault: true}, {ID: "2", Members: 1}}}}
	for _, tc := range []struct{ session, action, id string }{{"old", "create", ""}, {"current", "delete", "1"}, {"current", "delete", "2"}, {"current", "update", "missing"}} {
		if s.ManageChannel(tc.session, tc.action, tc.id, "name", "") == nil {
			t.Fatal("invalid mutation accepted")
		}
	}
	s.state.Session.CanManageChannels = false
	if s.ManageChannel("current", "create", "", "name", "") == nil || called != 0 {
		t.Fatal("permission guard bypassed")
	}
	s.state.Session.CanManageChannels = true
	c.mutate = func() error {
		called++
		if s.ManageChannel("current", "create", "", "duplicate", "") == nil {
			t.Error("concurrent mutation accepted")
		}
		s.mu.Lock()
		s.state.Session.ID = "new"
		s.mu.Unlock()
		return nil
	}
	if s.ManageChannel("current", "create", "", "name", "") == nil || called != 1 {
		t.Fatal("stale completion succeeded")
	}
	if s.channelMutationSession != "" {
		t.Fatal("pending gate leaked")
	}
}

type audioChannelConnection struct {
	channelConnection
	bitrate uint32
}

func (c *audioChannelConnection) ManageChannelAudio(_ context.Context, _, _, _, _ string, bitrate uint32) error {
	c.bitrate = bitrate
	return nil
}

func TestChannelAudioServiceRequiresCapabilityAndPreservesLegacyPath(t *testing.T) {
	legacyCalls := 0
	c := &audioChannelConnection{channelConnection: channelConnection{mutate: func() error { legacyCalls++; return nil }}}
	s := &Service{connection: c, state: Workspace{Session: Session{ID: "current", Mode: "connected", CanManageChannels: true}}}
	apply := func(b uint32) error {
		return s.ManageChannelContext(context.Background(), "current", "create", "", "Squad", "", b)
	}
	if apply(32000) == nil || c.bitrate != 0 {
		t.Fatal("old server accepted audio")
	}
	if err := apply(0); err != nil || legacyCalls != 1 {
		t.Fatal("legacy CRUD broken")
	}
	s.state.Session.CanConfigureChannelAudio = true
	for _, b := range []uint32{1, 15999, 16500, 64001, ^uint32(0)} {
		if apply(b) == nil {
			t.Fatal("invalid rate accepted")
		}
	}
	if err := apply(37000); err != nil || c.bitrate != 37000 || legacyCalls != 1 {
		t.Fatal("quality not dispatched atomically")
	}
}
