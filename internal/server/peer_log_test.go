package server

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	w "github.com/tsukiyoz/resona/internal/nativewire"
	"github.com/tsukiyoz/resona/internal/noiseudp"
)

func TestConnectionLogsFollowRestoredAccountAndNickname(t *testing.T) {
	var logs bytes.Buffer
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(oldLogger) })
	store := testStore(t, t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	const password = "not-a-real-password"
	a, err := store.accounts.Register(ctx, "log_test", password, "实际昵称")
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := store.accounts.Issue(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	key := []byte(strings.Repeat("k", 32))
	s, err := Listen("127.0.0.1:0", Config{Store: store, Name: "Log test", NoiseKey: key})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx) }()
	stop := func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("server did not join logging/connection workers")
		}
	}
	stopped := false
	t.Cleanup(func() {
		if !stopped {
			stop()
		}
	})
	public, err := noiseudp.PublicKey(key)
	if err != nil {
		t.Fatal(err)
	}
	c, err := w.DialNoise(ctx, s.Addr().String(), public)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.CloseWithError(0, "test done") })
	stream, err := c.OpenStreamSync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	hello := w.Hello{Nickname: "Resona"}
	if err := w.SignHello(c, private, &hello); err != nil {
		t.Fatal(err)
	}
	if err := w.Write(stream, w.HelloKind, 0, hello); err != nil {
		t.Fatal(err)
	}
	if frame, err := w.Read(stream); err != nil || frame.Kind != w.WelcomeKind {
		t.Fatalf("welcome: %v", err)
	}
	for i, command := range []w.AccountCommand{
		{Action: "token", Token: token},
		{Action: "update_profile", Nickname: "更新昵称🙂"},
	} {
		request := uint32(i + 1)
		if err := w.Write(stream, w.AccountKind, request, command); err != nil {
			t.Fatal(err)
		}
		for {
			frame, err := w.Read(stream)
			if err != nil {
				t.Fatal(err)
			}
			if frame.Kind != w.ReplyKind || frame.Request != request {
				continue
			}
			var reply w.Reply
			if err := w.Decode(frame, &reply); err != nil || reply.Code != w.OK {
				t.Fatalf("account reply code=%d error=%v", reply.Code, err)
			}
			break
		}
	}
	_ = c.CloseWithError(0, "test done")
	stop()
	stopped = true
	// Serve has joined all writers before inspecting the handler's buffer.
	if strings.Contains(logs.String(), token) || strings.Contains(logs.String(), password) {
		t.Fatal("credentials leaked to logs")
	}
	var events []map[string]any
	decoder := json.NewDecoder(&logs)
	for decoder.More() {
		var event map[string]any
		if err := decoder.Decode(&event); err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	if len(events) != 4 {
		t.Fatalf("got %d lifecycle events", len(events))
	}
	wantMessages := []string{"client connected", "client authenticated", "client profile updated", "client disconnected"}
	wantNames := []string{"Resona", "实际昵称", "更新昵称🙂", "更新昵称🙂"}
	for i, event := range events {
		if event["msg"] != wantMessages[i] || event["name"] != wantNames[i] || event["connection_name"] != "Resona" || event["session"] == "" || event["session"] != events[0]["session"] || event["member_id"] != events[0]["member_id"] || event["remote_ip"] != "127.0.0.1" {
			t.Fatalf("incorrect identity log: %v", event)
		}
		kind, uid, username := "account", a.UID, a.Username
		if i == 0 {
			kind, uid, username = "guest", "", ""
		}
		if event["identity_type"] != kind || event["account_uid"] != uid || event["account_name"] != username {
			t.Fatalf("incorrect account fields: %v", event)
		}
	}
	if events[1]["auth_method"] != "token" || events[2]["previous_name"] != "实际昵称" {
		t.Fatal("missing authentication/profile context")
	}
}
