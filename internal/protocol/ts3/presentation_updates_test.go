package ts3

import (
	"testing"

	teamspeak "github.com/honeybbq/teamspeak-go"
	"github.com/tsukiyoz/resona/internal/client"
)

func TestPresentationSurvivesPartialUpdatesAndReparenting(t *testing.T) {
	r := newReducer("uid")
	apply := func(name string, params map[string]string) {
		r.apply(teamspeak.IncomingCommand{Name: name, Params: params})
	}
	apply("channellist", map[string]string{"cid": "20", "cpid": "0", "channel_name": "[cspacer1]Heading", "channel_flag_permanent": "1", "channel_icon_id": "-1"})
	if ch := r.channels["20"]; ch.Kind != "separator" || ch.Name != "Heading" || ch.Align != "center" || ch.IconID != "4294967295" {
		t.Fatalf("initial presentation: %+v", ch)
	}
	apply("notifychanneledited", map[string]string{"cid": "20", "channel_topic": "updated"})
	if ch := r.channels["20"]; ch.Name != "Heading" || ch.Kind != "separator" {
		t.Fatalf("partial update lost raw metadata: %+v", ch)
	}
	apply("notifychannelmoved", map[string]string{"cid": "20", "cpid": "10"})
	if ch := r.channels["20"]; ch.Name != "[cspacer1]Heading" || ch.Kind != "channel" {
		t.Fatalf("nested channel incorrectly hidden: %+v", ch)
	}
	apply("notifychannelmoved", map[string]string{"cid": "20", "cpid": "0"})
	apply("notifychanneledited", map[string]string{"cid": "20", "channel_name": "[spacer1]"})
	if ch := r.channels["20"]; ch.Name != "" || ch.Kind != "separator" {
		t.Fatalf("blank spacer not normalized: %+v", ch)
	}
	apply("notifychanneledited", map[string]string{"cid": "20", "channel_flag_permanent": "0"})
	if ch := r.channels["20"]; ch.Name != "[spacer1]" || ch.Kind != "channel" {
		t.Fatalf("temporary ordinary channel hidden: %+v", ch)
	}
	apply("notifychanneldeleted", map[string]string{"cid": "20"})
	apply("notifychannelcreated", map[string]string{"cid": "20", "channel_name": "New channel"})
	if ch := r.channels["20"]; ch.Name != "New channel" || ch.IconID != "" || ch.Kind != "channel" {
		t.Fatalf("deleted metadata reused: %+v", ch)
	}
}

func TestIconPublicationDoesNotReplayMessagesAndCachesForNewChannels(t *testing.T) {
	r := newReducer("uid")
	r.initialized, r.listed = true, true
	r.state.SelfID = "7"
	r.users["7"] = client.User{ID: "7", ChannelID: "20"}
	r.channels["20"] = client.Channel{ID: "20", Name: "Room", IconID: "1234"}
	r.state.Messages = []client.RemoteMessage{{ChannelID: "20", UserID: "8", Text: "once"}}
	r.state.Events = []client.RemoteEvent{{Kind: "member_joined"}}
	var published client.RemoteState
	s := &connection{state: r, update: func(state client.RemoteState) { published = state }}
	s.publishIcon("1234", "opaque-reference")
	if len(published.Messages) != 0 || len(published.Events) != 0 || published.Channels[0].IconRef == "" {
		t.Fatal("icon update replayed transient events or omitted image")
	}
	r.apply(teamspeak.IncomingCommand{Name: "notifychannelcreated", Params: map[string]string{"cid": "21", "channel_name": "Another", "channel_icon_id": "1234"}})
	if r.channels["21"].IconRef == "" {
		t.Fatal("cached image not reused for new channel")
	}
	s.closing = true
	s.publishIcon("1234", "changed-after-close")
	if r.channels["20"].IconRef == "changed-after-close" {
		t.Fatal("closed connection accepted late icon")
	}
}
