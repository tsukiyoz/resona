package ts3

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	teamspeak "github.com/honeybbq/teamspeak-go"
	"github.com/tsukiyoz/resona/internal/client"
	"github.com/tsukiyoz/resona/internal/iconcache"
)

func detailConnection(t *testing.T) *connection {
	t.Helper()
	lifetime, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	r := newReducer("self-uid")
	r.apply(teamspeak.IncomingCommand{Name: "channellist", Params: map[string]string{"cid": "10", "channel_name": "Room", "channel_topic": "Topic", "channel_codec": "4"}})
	r.apply(teamspeak.IncomingCommand{Name: "notifycliententerview", Params: map[string]string{"clid": "1", "ctid": "10", "client_nickname": "Alice"}})
	r.state.MemberSyncState = "limited"
	return &connection{state: r, lifetime: lifetime, stop: stop, commandGate: make(chan struct{}, 1)}
}

func TestChannelDetailsSeparateTopicAndDescriptionAndPreserveUnknownLimits(t *testing.T) {
	s := detailConnection(t)
	s.execCommandResponse = func(_ context.Context, command string) ([]map[string]string, error) {
		if command != "channelinfo cid=10" {
			t.Fatalf("unexpected command %q", command)
		}
		return []map[string]string{{"channel_description": "[b]Description[/b]", "channel_codec_quality": "7", "channel_flag_permanent": "0", "channel_maxclients": "0"}}, nil
	}
	details, err := s.ReadChannelDetails(context.Background(), "10")
	if err != nil {
		t.Fatal(err)
	}
	if details.Topic == nil || *details.Topic != "Topic" || details.Description == nil || *details.Description != "[b]Description[/b]" || details.Codec == nil || *details.Codec != "Opus Voice" {
		t.Fatalf("lost details: %+v", details)
	}
	if details.CodecQuality == nil || *details.CodecQuality != 7 || details.Permanent == nil || *details.Permanent || details.MaxClients == nil || *details.MaxClients != 0 {
		t.Fatalf("lost known zero values: %+v", details)
	}
	if details.MaxClientsUnlimited != nil || details.MaxFamilyClients != nil || details.Members != 1 || details.MemberSyncState != "limited" {
		t.Fatalf("fabricated capacity or member count: %+v", details)
	}
	mergeChannelDetails(&details, map[string]string{"channel_codec_quality": "11", "channel_flag_permanent": "invalid", "channel_maxclients": "invalid"})
	if details.CodecQuality != nil || details.Permanent != nil || details.MaxClients != nil {
		t.Fatal("invalid metadata retained as known")
	}
}

func TestUserDetailsComeFromAuthorizedQuery(t *testing.T) {
	s := detailConnection(t)
	calls := 0
	s.execCommandResponse = func(_ context.Context, command string) ([]map[string]string, error) {
		calls++
		if command != "clientinfo clid=1" {
			t.Fatalf("unexpected command %q", command)
		}
		return []map[string]string{{"client_description": "Actual user description", "client_unique_identifier": "remote-uid", "client_input_muted": "0", "client_output_muted": "1"}}, nil
	}
	if _, err := s.ReadUserDetails(context.Background(), "99"); err == nil || calls != 0 {
		t.Fatal("queried a non-visible user")
	}
	details, err := s.ReadUserDetails(context.Background(), "1")
	if err != nil {
		t.Fatal(err)
	}
	if details.Description == nil || *details.Description != "Actual user description" || details.IdentityUID == nil || *details.IdentityUID != "remote-uid" || details.InputMuted == nil || *details.InputMuted || details.OutputMuted == nil || !*details.OutputMuted || details.Away != nil {
		t.Fatalf("incorrect user details: %+v", details)
	}
}

func TestDetailsCacheBoundsRequestsAndInvalidatesEntityVersion(t *testing.T) {
	s := detailConnection(t)
	calls := 0
	s.execCommandResponse = func(context.Context, string) ([]map[string]string, error) {
		calls++
		return []map[string]string{{"client_description": "description"}}, nil
	}
	for range 5 {
		if _, err := s.ReadUserDetails(context.Background(), "1"); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatalf("repeated selection queried %d times", calls)
	}
	s.mu.Lock()
	s.state.entityVersions["user:1"]++
	s.mu.Unlock()
	if _, err := s.ReadUserDetails(context.Background(), "1"); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("reused ID returned cached occupant")
	}
	s.mu.Lock()
	entry := s.detailCache["user:1"]
	entry.expires = time.Now().Add(-time.Second)
	s.detailCache["user:1"] = entry
	s.mu.Unlock()
	if _, err := s.ReadUserDetails(context.Background(), "1"); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatal("expired metadata did not refresh")
	}
}

func TestDetailQueryRejectsReusedClientID(t *testing.T) {
	s := detailConnection(t)
	s.execCommandResponse = func(_ context.Context, _ string) ([]map[string]string, error) {
		s.mu.Lock()
		s.state.apply(teamspeak.IncomingCommand{Name: "notifyclientleftview", Params: map[string]string{"clid": "1"}})
		s.state.apply(teamspeak.IncomingCommand{Name: "notifycliententerview", Params: map[string]string{"clid": "1", "ctid": "10", "client_nickname": "Alice"}})
		s.mu.Unlock()
		return []map[string]string{{"client_description": "Old occupant"}}, nil
	}
	if _, err := s.ReadUserDetails(context.Background(), "1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("reused client ID accepted stale details: %v", err)
	}
}

func TestDetailsCancellationAndPermissionError(t *testing.T) {
	s := detailConnection(t)
	s.execCommandResponse = func(_ context.Context, _ string) ([]map[string]string, error) {
		return nil, &teamspeak.CommandError{ID: 2568, Message: "secret server data"}
	}
	if _, err := s.ReadChannelDetails(context.Background(), "10"); err == nil || !strings.Contains(err.Error(), "2568") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("unsafe or missing permission error: %v", err)
	}
	s.commandGate <- struct{}{}
	done := make(chan error, 1)
	go func() { _, err := s.ReadChannelDetails(context.Background(), "10"); done <- err }()
	s.stop()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("unexpected cancellation error %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("detail query survived connection cancellation")
	}
}

func TestIconResourceIsAuthorizedAndAbsentFromSnapshots(t *testing.T) {
	s := detailConnection(t)
	s.iconStore = iconcache.New("")
	data, err := rasterIconDataURL(testIcon(t, "png", 16))
	if err != nil {
		t.Fatal(err)
	}
	ref, err := s.iconStore.Reference(context.Background(), iconcache.Key{Protocol: "ts3", IdentityUID: "owner", IconID: "1000"}, func(context.Context) (string, error) { return data, nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadIconResource(context.Background(), ref); err == nil {
		t.Fatal("unadvertised reference bypassed session authorization")
	}
	channel := s.state.channels["10"]
	channel.IconRef = ref
	s.state.channels["10"] = channel
	resource, err := s.ReadIconResource(context.Background(), ref)
	if err != nil || resource.DataURL != data {
		t.Fatalf("resource fetch failed: %v", err)
	}
	for range 3 {
		snapshot := s.state.snapshot()
		encoded, err := json.Marshal(client.Workspace{Channels: snapshot.Channels})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), "base64") || strings.Contains(string(encoded), "iconDataURL") || !strings.Contains(string(encoded), ref) {
			t.Fatal("workspace included icon body or omitted reference")
		}
	}
	s.closing = true
	if _, err := s.ReadIconResource(context.Background(), ref); err == nil {
		t.Fatal("closed session retained resource access")
	}
}
