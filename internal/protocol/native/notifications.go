package native

import (
	"github.com/tsukiyoz/resona/internal/client"
	w "github.com/tsukiyoz/resona/internal/nativewire"
)

// Every scope includes current-channel members. Compare each ordered state before
// UI coalescing, so a short visit retains both events and list changes stay silent.
func memberEvents(before, after w.State) []client.RemoteEvent {
	channelOf := func(s w.State) uint16 {
		for _, member := range s.Members {
			if member.ID == s.Self {
				return member.Channel
			}
		}
		return 0
	}
	channel := channelOf(after)
	if before.Self == 0 || before.Self != after.Self || channel == 0 || channelOf(before) != channel {
		return nil
	}
	old := make(map[uint16]string, len(before.Members))
	next := make(map[uint16]string, len(after.Members))
	for _, m := range before.Members {
		if m.ID != before.Self && m.Channel == channel {
			old[m.ID] = m.Instance
		}
	}
	for _, m := range after.Members {
		if m.ID != after.Self && m.Channel == channel {
			next[m.ID] = m.Instance
		}
	}
	var events []client.RemoteEvent
	for _, m := range before.Members {
		if instance, ok := old[m.ID]; ok && next[m.ID] != instance {
			events = append(events, client.RemoteEvent{Kind: "member_left", UserID: id(m.ID), ChannelID: id(channel)})
		}
	}
	for _, m := range after.Members {
		if instance, ok := next[m.ID]; ok && old[m.ID] != instance {
			events = append(events, client.RemoteEvent{Kind: "member_joined", UserID: id(m.ID), ChannelID: id(channel)})
		}
	}
	return events
}
