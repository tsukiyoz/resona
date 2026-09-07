package ts3

import (
	"context"
	"testing"

	teamspeak "github.com/honeybbq/teamspeak-go"
	"github.com/tsukiyoz/resona/internal/client"
)

func notificationReducer() *reducer {
	r := newReducer("uid")
	r.apply(teamspeak.IncomingCommand{Name: "initserver", Params: map[string]string{"aclid": "7"}})
	r.apply(teamspeak.IncomingCommand{Name: "channellistfinished"})
	r.apply(teamspeak.IncomingCommand{Name: "notifycliententerview", Params: map[string]string{"clid": "7", "ctid": "10", "reasonid": "2"}})
	return r
}

func TestMemberNotificationsFollowLiveProtocolEvents(t *testing.T) {
	r := notificationReducer()
	steps := []struct {
		name, id, channel, reason, want string
	}{
		{"notifycliententerview", "8", "10", "2", ""}, // Existing members after our own ready signal.
		{"notifycliententerview", "9", "10", "0", "member_joined"},
		{"notifycliententerview", "9", "10", "0", ""},
		{"notifyclientupdated", "9", "10", "0", ""},
		{"notifyclientmoved", "9", "20", "0", "member_left"},
		{"notifyclientmoved", "9", "20", "0", ""},
		{"notifyclientmoved", "9", "10", "1", "member_joined"},
		{"notifyclientleftview", "9", "0", "8", "member_left"},
		{"notifyclientleftview", "9", "0", "8", ""},
		{"notifycliententerview", "10", "20", "0", ""},
		{"notifyclientleftview", "10", "0", "8", ""},
		{"notifyclientleftview", "8", "0", "2", ""},
		{"notifycliententerview", "11", "10", "", ""},
		{"notifyclientleftview", "11", "0", "99", ""},
		{"notifyclientmoved", "7", "20", "0", ""},
		{"notifycliententerview", "12", "20", "2", ""}, // Destination membership snapshot.
		{"notifycliententerview", "13", "10", "0", ""},
		{"notifycliententerview", "14", "20", "0", "member_joined"},
		{"notifyclientleftview", "14", "0", "3", "member_left"},
	}
	for i, step := range steps {
		r.apply(teamspeak.IncomingCommand{Name: step.name, Params: map[string]string{"clid": step.id, "ctid": step.channel, "reasonid": step.reason}})
		events := r.snapshot().Events
		if step.want == "" {
			if len(events) != 0 {
				t.Fatalf("step %d: unexpected events %+v", i, events)
			}
			continue
		}
		if len(events) != 1 || events[0].Kind != step.want || events[0].UserID != step.id || events[0].ChannelID != r.snapshot().ChannelID {
			t.Fatalf("step %d: want %s for %s, got %+v", i, step.want, step.id, events)
		}
		events[0].Kind = "mutated"
		if r.snapshot().Events[0].Kind != step.want {
			t.Fatal("event snapshot aliases reducer memory")
		}
	}
	r.apply(teamspeak.IncomingCommand{Name: "notifychanneledited", Params: map[string]string{"cid": "20", "channel_topic": "new"}})
	if len(r.snapshot().Events) != 0 {
		t.Fatal("an unrelated update replayed the previous event")
	}
}

func TestMemberNotificationsSilentBeforeReady(t *testing.T) {
	r := newReducer("uid")
	r.apply(teamspeak.IncomingCommand{Name: "notifycliententerview", Params: map[string]string{"clid": "8", "ctid": "10", "reasonid": "0"}})
	if len(r.snapshot().Events) != 0 {
		t.Fatal("initial membership produced a live event")
	}
}

func TestOwnExitReasonsCloseWithoutMemberNotification(t *testing.T) {
	for _, reason := range []string{"3", "5", "6", "7", "8", "11"} {
		t.Run(reason, func(t *testing.T) {
			r := notificationReducer()
			r.apply(teamspeak.IncomingCommand{Name: "notifyclientleftview", Params: map[string]string{"clid": "7", "reasonid": reason}})
			if !r.snapshot().Closed || len(r.snapshot().Events) != 0 {
				t.Fatalf("own exit should close without a member event: %+v", r.snapshot())
			}
		})
	}
}

func TestTransportDisconnectDoesNotReplayLastMemberEvent(t *testing.T) {
	s := newMoveTestConnection(t, func(context.Context, string) error { return nil })
	var updates []client.RemoteState
	s.update = func(state client.RemoteState) { updates = append(updates, state) }
	s.observe(teamspeak.IncomingCommand{Name: "notifycliententerview", Params: map[string]string{"clid": "8", "ctid": "1", "reasonid": "0"}})
	s.disconnected()
	s.disconnected()
	if len(updates) != 2 || len(updates[0].Events) != 1 || !updates[1].Closed || len(updates[1].Events) != 0 {
		t.Fatalf("disconnect duplicated or replayed membership events: %+v", updates)
	}
}
