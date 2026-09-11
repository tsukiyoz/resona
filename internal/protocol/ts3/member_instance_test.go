package ts3

import (
	teamspeak "github.com/honeybbq/teamspeak-go"
	"testing"
)

func TestMemberInstanceSurvivesMovesButNotReentry(t *testing.T) {
	r := newReducer("self")
	apply := func(name string) {
		r.apply(teamspeak.IncomingCommand{Name: name, Params: map[string]string{"clid": "2", "ctid": "10", "client_nickname": "same"}})
	}
	apply("notifycliententerview")
	instance := r.users["2"].Instance
	if instance == "" {
		t.Fatal("member instance missing")
	}
	apply("notifyclientupdated")
	apply("notifyclientmoved")
	if r.users["2"].Instance != instance {
		t.Fatal("same member changed instance")
	}
	apply("notifyclientleftview")
	apply("notifycliententerview")
	if r.users["2"].Instance == instance {
		t.Fatal("reused ID inherited departed member instance")
	}
}
