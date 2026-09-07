package ts3

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	teamspeak "github.com/honeybbq/teamspeak-go"
	"github.com/tsukiyoz/resona/internal/client"
)

func TestMemberSubscriptionDoesNotBlockObserverAndRunsOnce(t *testing.T) {
	sent, ack := make(chan string, 1), make(chan struct{})
	var calls atomic.Int32
	s := newMoveTestConnection(t, func(ctx context.Context, raw string) error {
		calls.Add(1)
		sent <- raw
		select {
		case <-ack:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	updates := make(chan client.RemoteState, 10)
	s.update = func(state client.RemoteState) { updates <- state }
	s.state.state.Events = []client.RemoteEvent{{Kind: "member_joined", UserID: "8", ChannelID: "1"}}
	s.startMemberSubscription()
	s.startMemberSubscription()
	if initial := <-updates; initial.MemberSyncState != "pending" || len(initial.Events) != 0 {
		t.Fatalf("bad subscription start: %+v", initial)
	}
	if raw := <-sent; raw != "channelsubscribeall" {
		t.Fatalf("unexpected command: %q", raw)
	}
	// A subscription snapshot can arrive before the command acknowledgment.
	s.observe(teamspeak.IncomingCommand{Name: "notifycliententerview", Params: map[string]string{
		"clid": "8", "ctid": "2", "client_nickname": "Other member", "reasonid": "2",
	}})
	view := <-updates
	if view.MemberSyncState != "pending" || len(view.Users) != 2 || len(view.Events) != 0 {
		t.Fatalf("subscription member was missing or produced a join cue: %+v", view)
	}
	close(ack)
	select {
	case complete := <-updates:
		if complete.MemberSyncState != "ready" || complete.MemberSyncError != "" || len(complete.Users) != 2 || len(complete.Events) != 0 {
			t.Fatalf("bad subscription completion: %+v", complete)
		}
	case <-time.After(time.Second):
		t.Fatal("subscription did not complete")
	}
	if calls.Load() != 1 {
		t.Fatal("subscription command repeated")
	}
}

func TestMemberSubscriptionFailureKeepsVisibleStateAndSanitizesError(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		text string
	}{
		{"permission", &teamspeak.CommandError{ID: 2568, Message: "private server response"}, "2568"},
		{"timeout", context.DeadlineExceeded, "超时"},
		{"transport", errors.New("private server response"), "未完成"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newMoveTestConnection(t, func(context.Context, string) error { return tc.err })
			updates := make(chan client.RemoteState, 2)
			s.update = func(state client.RemoteState) { updates <- state }
			s.startMemberSubscription()
			<-updates
			select {
			case failed := <-updates:
				if failed.MemberSyncState != "limited" || !strings.Contains(failed.MemberSyncError, tc.text) || strings.Contains(failed.MemberSyncError, "private") {
					t.Fatalf("bad subscription error: %+v", failed)
				}
				if failed.Closed || failed.Error != "" || len(failed.Channels) != 2 || len(failed.Users) != 1 {
					t.Fatalf("subscription failure damaged connected state: %+v", failed)
				}
			case <-time.After(time.Second):
				t.Fatal("subscription did not report its error")
			}
		})
	}
}

func TestCloseCancelsAndJoinsMemberSubscription(t *testing.T) {
	sent, exited := make(chan struct{}), make(chan struct{})
	s := newMoveTestConnection(t, func(ctx context.Context, _ string) error {
		close(sent)
		<-ctx.Done()
		close(exited)
		return ctx.Err()
	})
	updates := make(chan client.RemoteState, 2)
	s.update = func(state client.RemoteState) { updates <- state }
	s.startMemberSubscription()
	<-updates
	<-sent
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-exited:
	default:
		t.Fatal("Close returned with subscription still running")
	}
	s.startMemberSubscription()
	select {
	case state := <-updates:
		t.Fatalf("closed session published a late subscription update: %+v", state)
	default:
	}
}

func TestMemberSubscriptionWaitsForInitialView(t *testing.T) {
	s := newMoveTestConnection(t, func(context.Context, string) error { t.Error("subscription before initial view"); return nil })
	s.state.listed = false
	s.startMemberSubscription()
	if s.subscribeStarted {
		t.Fatal("subscription started before channel list completed")
	}
}
