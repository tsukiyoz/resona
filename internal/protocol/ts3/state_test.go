package ts3

import (
	"testing"

	teamspeak "github.com/honeybbq/teamspeak-go"
)

func TestInitialSnapshotAndOrderedUpdates(t *testing.T) {
	r := newReducer("public-uid")
	apply := func(name string, params map[string]string) {
		r.apply(teamspeak.IncomingCommand{Name: name, Params: params})
	}
	apply("notifycliententerview", map[string]string{"clid": "7", "ctid": "20", "client_nickname": "same"})
	apply("notifycliententerview", map[string]string{"clid": "8", "ctid": "20", "client_nickname": "same"})
	apply("channellist", map[string]string{"cid": "20", "cpid": "10", "channel_name": `Music\sRoom`, "channel_topic": "topic", "channel_order": "0"})
	apply("channellist", map[string]string{"cid": "10", "cpid": "0", "channel_name": "Lobby"})
	apply("channellistfinished", nil)
	if r.ready() {
		t.Fatal("ready before initserver")
	}
	apply("initserver", map[string]string{"aclid": "7", "virtualserver_name": "Server"})
	if !r.ready() {
		t.Fatal("not ready after complete initial channel list and initserver")
	}
	s := r.snapshot()
	if s.ServerName != "Server" || s.SelfID != "7" || s.ChannelID != "20" || s.IdentityUID != "public-uid" {
		t.Fatalf("bad session: %+v", s)
	}
	if len(s.Users) != 2 || !s.Users[0].Self || s.Users[1].Self {
		t.Fatalf("self matched by nickname: %+v", s.Users)
	}
	if s.Channels[1].ParentID != "10" || s.Channels[1].Members != 2 || s.Channels[1].Name != `Music\sRoom` {
		t.Fatalf("bad channels: %+v", s.Channels)
	}
	s.Channels[1].Name = "mutated"
	s.Users[0].Nickname = "mutated"
	apply("notifychanneledited", map[string]string{"cid": "20", "channel_topic": "new topic"})
	apply("notifyclientmoved", map[string]string{"clid": "7", "ctid": "10"})
	apply("notifyclientupdated", map[string]string{"clid": "7", "client_nickname": "new name"})
	s = r.snapshot()
	if s.ChannelID != "10" || s.Channels[1].Name != `Music\sRoom` || s.Channels[1].Members != 1 || s.Users[0].Nickname != "new name" {
		t.Fatalf("partial update lost fields: %+v", s)
	}
	apply("notifyclientleftview", map[string]string{"clid": "8"})
	apply("notifychannelmoved", map[string]string{"cid": "20", "cpid": "0", "order": "10"})
	s = r.snapshot()
	if len(s.Users) != 1 || s.Channels[1].ParentID != "0" || s.Channels[1].Order != "10" {
		t.Fatalf("bad move/leave: %+v", s)
	}
	apply("notifychanneldeleted", map[string]string{"cid": "20"})
	if len(r.snapshot().Channels) != 1 {
		t.Fatal("delete ignored")
	}
	apply("notifyclientleftview", map[string]string{"clid": "7", "reasonid": "5"})
	if !r.snapshot().Closed {
		t.Fatal("own departure did not end session")
	}
}

func TestDelayedSelfAndChannelKick(t *testing.T) {
	r := newReducer("uid")
	apply := func(name string, params map[string]string) {
		r.apply(teamspeak.IncomingCommand{Name: name, Params: params})
	}
	apply("initserver", map[string]string{"aclid": "7"})
	apply("error", map[string]string{"id": "2568"})
	if r.state.Closed {
		t.Fatal("post-login command error ended connection")
	}
	apply("channellist", map[string]string{"cid": "10", "channel_name": "Lobby"})
	apply("channellistfinished", nil)
	if r.ready() {
		t.Fatal("ready without own membership")
	}
	apply("notifycliententerview", map[string]string{"clid": "7", "ctid": "10", "client_nickname": "self"})
	if !r.ready() {
		t.Fatal("own membership did not finish setup")
	}
	apply("notifyclientleftview", map[string]string{"clid": "7", "reasonid": "4"})
	if r.state.Closed {
		t.Fatal("channel kick ended entire connection")
	}
	apply("notifycliententerview", map[string]string{"clid": "7", "ctid": "20", "client_nickname": "self"})
	if r.snapshot().ChannelID != "20" {
		t.Fatal("did not follow own channel after channel kick")
	}
}

func TestInitialFailureSanitized(t *testing.T) {
	r := newReducer("uid")
	r.apply(teamspeak.IncomingCommand{Name: "error", Params: map[string]string{"id": "2568", "msg": "secret server data"}})
	if !r.state.Closed || r.state.Error != "服务器拒绝连接（错误码 2568）" {
		t.Fatalf("bad failure: %+v", r.state)
	}
}
