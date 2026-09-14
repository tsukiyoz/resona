package native

import (
	"context"
	"reflect"
	"testing"

	"github.com/tsukiyoz/resona/internal/client"
	w "github.com/tsukiyoz/resona/internal/nativewire"
)

func TestMemberEventsIgnoreBootstrapScopesMetadataAndOwnMove(t *testing.T) {
	self := w.Member{ID: 1, Channel: 1, Instance: "self"}
	other := w.Member{ID: 2, Channel: 1, Instance: "other"}
	base := w.State{Self: 1, Members: []w.Member{self, other}}
	if got := memberEvents(w.State{}, base); len(got) != 0 {
		t.Fatal("bootstrap generated events")
	}
	expanded := base
	expanded.AllMembers = true
	expanded.Members = append(append([]w.Member{}, base.Members...), w.Member{ID: 3, Channel: 2, Instance: "outside"})
	if len(memberEvents(base, expanded)) != 0 || len(memberEvents(expanded, base)) != 0 {
		t.Fatal("scope generated events")
	}
	changed := base
	changed.Members = append([]w.Member{}, base.Members...)
	changed.Members[1].Muted = true
	changed.Members[1].Nickname = "renamed"
	if len(memberEvents(base, changed)) != 0 {
		t.Fatal("metadata generated events")
	}
	changed.Members[0].Channel = 2
	if len(memberEvents(base, changed)) != 0 {
		t.Fatal("own move generated events")
	}
	changed.Members[0].Channel = 1
	changed.Members[1].Instance = "replacement"
	want := []client.RemoteEvent{{Kind: "member_left", UserID: "2", ChannelID: "1"}, {Kind: "member_joined", UserID: "2", ChannelID: "1"}}
	if got := memberEvents(base, changed); !reflect.DeepEqual(got, want) {
		t.Fatalf("instance replacement: %+v", got)
	}
}

func TestLiveMemberNotificationsSurviveScopeChangesAndShortVisits(t *testing.T) {
	profile, stop, done := startServer(t)
	defer func() { stop(); <-done }()
	a, seen := connectTest(t, profile)
	ctx := context.Background()
	events := func() []client.RemoteEvent {
		seen.mu.Lock()
		defer seen.mu.Unlock()
		return append([]client.RemoteEvent{}, seen.events...)
	}
	barrier := func() {
		t.Helper()
		if err := a.SetResourceInterest(ctx, false, false); err != nil {
			t.Fatal(err)
		}
		if err := a.SetResourceInterest(ctx, true, true); err != nil {
			t.Fatal(err)
		}
	}
	if len(events()) != 0 {
		t.Fatal("initial list sounded")
	}
	b, bseen := connectTest(t, profile)
	bseen.mu.Lock()
	initialEvents := len(bseen.events)
	bseen.mu.Unlock()
	if initialEvents != 0 {
		t.Fatal("new arrival heard existing members join")
	}
	barrier()
	if got := events(); len(got) != 1 || got[0].Kind != "member_joined" {
		t.Fatalf("join: %+v", got)
	}
	if err := b.MoveChannel(ctx, "2"); err != nil {
		t.Fatal(err)
	}
	barrier()
	if got := events(); len(got) != 2 || got[1].Kind != "member_left" {
		t.Fatalf("leave: %+v", got)
	}
	if err := a.MoveChannel(ctx, "2"); err != nil {
		t.Fatal(err)
	}
	barrier()
	if len(events()) != 2 {
		t.Fatal("own move or new scope sounded")
	}
	visitor, _ := connectTest(t, profile)
	if err := visitor.MoveChannel(ctx, "2"); err != nil {
		t.Fatal(err)
	}
	if err := visitor.Close(); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return len(events()) == 4 })
	got := events()
	if got[2].Kind != "member_joined" || got[3].Kind != "member_left" || got[2].UserID != got[3].UserID || got[2].ChannelID != "2" {
		t.Fatalf("short visit: %+v", got)
	}
}
