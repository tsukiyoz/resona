//go:build integration

package teamspeak_test

// Integration tests against a live TeamSpeak 3 server (build tag: integration).
//
// Run locally:
//
//	docker compose -f docker-compose.integration.yml up -d --wait
//	TEAMSPEAK_ADDR=127.0.0.1:9987 go test -tags integration ./... -v -timeout 120s
//	docker compose -f docker-compose.integration.yml down
//
// In CI the server is provided by the workflow's service container and
// TEAMSPEAK_ADDR is set automatically.
//
// # Notes
//
//   - A single shared client is reused across tests to avoid TS3 anti-flood
//     protection, which bans IPs that establish too many connections quickly.
//   - Some commands (clientlist, channellist) require elevated server group
//     permissions that the default "Guest" group does not have.  Those tests
//     skip automatically instead of failing hard.

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	teamspeak "github.com/honeybbq/teamspeak-go"
	"github.com/honeybbq/teamspeak-go/crypto"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

var (
	sharedClient      *teamspeak.Client
	sharedClientEnter chan teamspeak.ClientInfo
	sharedOnce        sync.Once
	sharedErr         error
)

var (
	integrationServerPassword         = os.Getenv("TEAMSPEAK_SERVER_PASSWORD")
	integrationDefaultChannel         = os.Getenv("TEAMSPEAK_DEFAULT_CHANNEL")
	integrationDefaultChannelPassword = os.Getenv("TEAMSPEAK_DEFAULT_CHANNEL_PASSWORD")
)

func integrationClientOptions(logger *slog.Logger) []teamspeak.ClientOption {
	opts := make([]teamspeak.ClientOption, 0, 4)
	if logger != nil {
		opts = append(opts, teamspeak.WithLogger(logger))
	}
	if integrationServerPassword != "" {
		opts = append(opts, teamspeak.WithServerPassword(integrationServerPassword))
	}
	if integrationDefaultChannel != "" {
		opts = append(opts, teamspeak.WithDefaultChannel(integrationDefaultChannel))
	}
	if integrationDefaultChannelPassword != "" {
		opts = append(opts, teamspeak.WithDefaultChannelPassword(integrationDefaultChannelPassword))
	}

	return opts
}

func requireTeamSpeakAddr(t *testing.T) string {
	t.Helper()
	addr := os.Getenv("TEAMSPEAK_ADDR")
	if addr == "" {
		t.Skip("TEAMSPEAK_ADDR not set — skip integration test (set TEAMSPEAK_ADDR=host:port to enable)")
	}
	return addr
}

func requireSharedClient(t *testing.T) *teamspeak.Client {
	t.Helper()
	addr := requireTeamSpeakAddr(t)

	sharedOnce.Do(func() {
		id, err := crypto.GenerateIdentity(8)
		if err != nil {
			sharedErr = err
			return
		}
		selfUID := crypto.GetUidFromPublicKey(id.PublicKeyBase64())
		c := teamspeak.NewClient(id, addr, "teamspeak-go-integ", integrationClientOptions(nil)...)
		sharedClientEnter = make(chan teamspeak.ClientInfo, 1)
		c.OnClientEnter(func(info teamspeak.ClientInfo) {
			if info.UID == selfUID {
				select {
				case sharedClientEnter <- info:
				default:
				}
			}
		})
		if err = c.Connect(); err != nil {
			sharedErr = err
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err = c.WaitConnected(ctx); err != nil {
			sharedErr = err
			return
		}
		sharedClient = c
	})

	if sharedErr != nil {
		t.Fatalf("shared client setup failed: %v", sharedErr)
	}
	return sharedClient
}

func skipOnPermErr(t *testing.T, err error) {
	t.Helper()
	if err != nil && strings.Contains(err.Error(), "insufficient") {
		t.Skipf("skipping — server returned permission error: %v", err)
	}
}

func newConnectedIntegrationClient(t *testing.T, addr string, nicknamePrefix string, logger *slog.Logger) *teamspeak.Client {
	t.Helper()

	id, err := crypto.GenerateIdentity(8)
	if err != nil {
		t.Fatalf("GenerateIdentity: %v", err)
	}

	nickname := nicknamePrefix + strconv.FormatInt(time.Now().UTC().UnixNano()%1_000_000, 10)

	// TS3 anti-flood may temporarily ban IPs that connect too frequently.
	// Retry with backoff to handle transient bans in CI.
	var client *teamspeak.Client
	for attempt := range 3 {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt*5) * time.Second)
		}
		client = teamspeak.NewClient(id, addr, nickname, integrationClientOptions(logger)...)
		if err = client.Connect(); err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		err = client.WaitConnected(ctx)
		cancel()
		if err == nil {
			break
		}
		_ = client.Disconnect()
	}

	if err != nil {
		t.Fatalf("WaitConnected(%s) after retries: %v", nickname, err)
	}

	t.Cleanup(func() {
		_ = client.Disconnect()
	})

	return client
}

func mapKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func extractJSONField(s string, needle string) string {
	idx := strings.Index(s, needle)
	if idx < 0 {
		return ""
	}
	start := idx + len(needle)
	end := strings.Index(s[start:], "\"")
	if end < 0 {
		return ""
	}
	return s[start : start+end]
}

// ---------------------------------------------------------------------------
// Connection
// ---------------------------------------------------------------------------

func TestIntegration_Connect(t *testing.T) {
	c := requireSharedClient(t)

	clid := c.ClientID()
	if clid == 0 {
		t.Error("expected non-zero client ID after connect")
	}
	t.Logf("connected: clid=%d", clid)
}

func TestIntegration_ConnectWithOptionalHandshakeAuth(t *testing.T) {
	c := requireSharedClient(t)

	t.Logf(
		"connect auth enabled: serverPassword=%t defaultChannel=%t defaultChannelPassword=%t",
		integrationServerPassword != "",
		integrationDefaultChannel != "",
		integrationDefaultChannelPassword != "",
	)
	if c.ClientID() == 0 {
		t.Error("expected non-zero client ID after connect")
	}
}

func TestIntegration_ClientEnterChannelID(t *testing.T) {
	requireSharedClient(t)

	select {
	case info := <-sharedClientEnter:
		t.Logf("clientEnter: clid=%d cid=%d", info.ID, info.ChannelID)
		if info.ChannelID == 0 {
			t.Error("expected clientEnter to report a non-zero channel ID")
		}
	case <-time.After(8 * time.Second):
		t.Fatal("timed out waiting for clientEnter")
	}
}

func TestIntegration_Disconnect(t *testing.T) {
	addr := requireTeamSpeakAddr(t)

	id, err := crypto.GenerateIdentity(8)
	if err != nil {
		t.Fatalf("GenerateIdentity: %v", err)
	}
	c := teamspeak.NewClient(id, addr, "teamspeak-go-integ-disc", integrationClientOptions(nil)...)

	if err = c.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err = c.WaitConnected(ctx); err != nil {
		t.Fatalf("WaitConnected: %v", err)
	}

	disconnected := make(chan error, 1)
	c.OnDisconnected(func(e error) { disconnected <- e })

	if err = c.Disconnect(); err != nil {
		t.Logf("Disconnect returned (non-fatal): %v", err)
	}

	select {
	case <-disconnected:
	case <-time.After(5 * time.Second):
		t.Error("OnDisconnected not fired after Disconnect()")
	}
}

// ---------------------------------------------------------------------------
// Server queries
// ---------------------------------------------------------------------------

func TestIntegration_ListClients(t *testing.T) {
	c := requireSharedClient(t)

	clients, err := c.ListClients()
	skipOnPermErr(t, err)
	if err != nil {
		t.Fatalf("ListClients: %v", err)
	}
	if len(clients) == 0 {
		t.Fatal("expected at least one client (ourselves)")
	}

	ownID := c.ClientID()
	found := false
	for _, cl := range clients {
		if cl.ID == ownID {
			found = true
			t.Logf("self: clid=%d nick=%q cid=%d", cl.ID, cl.Nickname, cl.ChannelID)
			break
		}
	}
	if !found {
		t.Errorf("own clid=%d not found in clientlist", ownID)
	}
}

func TestIntegration_ListChannels(t *testing.T) {
	c := requireSharedClient(t)

	channels, err := c.ListChannels()
	skipOnPermErr(t, err)
	if err != nil {
		t.Fatalf("ListChannels: %v", err)
	}
	if len(channels) == 0 {
		t.Fatal("expected at least one channel (default channel)")
	}
	t.Logf("channels: %d found, first=%q", len(channels), channels[0].Name)
}

func TestIntegration_JoinsConfiguredDefaultChannel(t *testing.T) {
	if integrationDefaultChannel == "" {
		t.Skip("TEAMSPEAK_DEFAULT_CHANNEL not set")
	}

	c := requireSharedClient(t)

	channels, err := c.ListChannels()
	skipOnPermErr(t, err)
	if err != nil {
		t.Fatalf("ListChannels: %v", err)
	}

	clients, err := c.ListClients()
	skipOnPermErr(t, err)
	if err != nil {
		t.Fatalf("ListClients: %v", err)
	}

	var self *teamspeak.ClientInfo
	for i := range clients {
		if clients[i].ID == c.ClientID() {
			self = &clients[i]
			break
		}
	}
	if self == nil {
		t.Fatal("expected to find ourselves in client list")
	}

	var currentChannel *teamspeak.ChannelInfo
	for i := range channels {
		if channels[i].ID == self.ChannelID {
			currentChannel = &channels[i]
			break
		}
	}
	if currentChannel == nil {
		t.Fatalf("expected to resolve current channel for cid=%d", self.ChannelID)
	}
	if currentChannel.Name != integrationDefaultChannel {
		t.Fatalf("expected current channel %q, got %q", integrationDefaultChannel, currentChannel.Name)
	}
}

func TestIntegration_GetClientInfo(t *testing.T) {
	c := requireSharedClient(t)

	info, err := c.GetClientInfo(c.ClientID())
	skipOnPermErr(t, err)
	if err != nil {
		t.Fatalf("GetClientInfo: %v", err)
	}
	if len(info) == 0 {
		t.Fatal("expected non-empty client info map")
	}
	if info["client_nickname"] == "" {
		t.Errorf("expected client_nickname in clientinfo response; got keys: %v", mapKeys(info))
	}
	t.Logf("clientinfo keys: %v", mapKeys(info))
}

// ---------------------------------------------------------------------------
// Text messages
// ---------------------------------------------------------------------------

func TestIntegration_TextPrivateNotifyFields(t *testing.T) {
	addr := requireTeamSpeakAddr(t)

	var logBuf bytes.Buffer
	debugLogger := slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	receiver := newConnectedIntegrationClient(t, addr, "rx", debugLogger)

	sender := newConnectedIntegrationClient(t, addr, "tx", slog.Default())
	senderInfo, err := sender.GetClientInfo(sender.ClientID())
	if err != nil {
		t.Fatalf("sender GetClientInfo: %v", err)
	}
	senderUID := strings.TrimSpace(senderInfo["client_unique_identifier"])
	t.Logf("sender clientinfo keys: %v", mapKeys(senderInfo))

	received := make(chan teamspeak.TextMessage, 1)
	receiver.OnTextMessage(func(msg teamspeak.TextMessage) {
		select {
		case received <- msg:
		default:
		}
	})

	time.Sleep(500 * time.Millisecond)

	probeText := fmt.Sprintf("cursor-probe-%d", time.Now().UTC().UnixNano())
	if err := sender.SendTextMessage(1, uint64(receiver.ClientID()), probeText); err != nil {
		t.Fatalf("sender SendTextMessage private: %v", err)
	}

	var msg teamspeak.TextMessage
	select {
	case msg = <-received:
	case <-time.After(10 * time.Second):
		t.Fatalf("timeout waiting for private text notification; logs=%s", logBuf.String())
	}

	if msg.Message != probeText {
		t.Fatalf("unexpected message text: got %q want %q", msg.Message, probeText)
	}

	logs := logBuf.String()
	t.Logf("receiver logs: %s", logs)

	if !strings.Contains(logs, "\"name\":\"notifytextmessage\"") {
		t.Fatalf("expected notifytextmessage in logs, got: %s", logs)
	}

	targetNeedle := fmt.Sprintf("\"target\":\"%d\"", receiver.ClientID())
	if !strings.Contains(logs, targetNeedle) {
		t.Fatalf("expected raw notify target %s in logs, got: %s", targetNeedle, logs)
	}

	rawInvokerUID := extractJSONField(logs, "\"invokeruid\":\"")
	if rawInvokerUID == "" {
		t.Fatalf("expected raw notify invokeruid in logs, got: %s", logs)
	}

	if msg.TargetMode != 1 {
		t.Fatalf("unexpected target mode: got %d want 1", msg.TargetMode)
	}

	if msg.TargetID != uint64(receiver.ClientID()) {
		t.Fatalf("parsed TargetID mismatch: got %d want %d", msg.TargetID, receiver.ClientID())
	}

	expectedInvokerUID := rawInvokerUID
	if senderUID != "" {
		expectedInvokerUID = senderUID
	}
	if strings.TrimSpace(msg.InvokerUID) != expectedInvokerUID {
		t.Fatalf("parsed InvokerUID mismatch: got %q want %q", msg.InvokerUID, expectedInvokerUID)
	}
}

// ---------------------------------------------------------------------------
// Poke
// ---------------------------------------------------------------------------

func TestIntegration_PokeSendAndReceive(t *testing.T) {
	addr := requireTeamSpeakAddr(t)

	sender := newConnectedIntegrationClient(t, addr, "poke-tx", slog.Default())
	receiver := newConnectedIntegrationClient(t, addr, "poke-rx", slog.Default())

	pokeMsg := fmt.Sprintf("poke-test-%d", time.Now().UTC().UnixNano())

	poked := make(chan teamspeak.PokeEvent, 1)
	receiver.OnPoked(func(e teamspeak.PokeEvent) {
		select {
		case poked <- e:
		default:
		}
	})

	time.Sleep(500 * time.Millisecond)

	if err := sender.Poke(receiver.ClientID(), pokeMsg); err != nil {
		t.Fatalf("Poke: %v", err)
	}
	t.Logf("sent poke from clid=%d to clid=%d msg=%q", sender.ClientID(), receiver.ClientID(), pokeMsg)

	select {
	case evt := <-poked:
		t.Logf("poke received: invoker=%q uid=%q msg=%q", evt.InvokerName, evt.InvokerUID, evt.Message)
		if evt.InvokerID != sender.ClientID() {
			t.Errorf("InvokerID mismatch: got %d want %d", evt.InvokerID, sender.ClientID())
		}
		if evt.Message != pokeMsg {
			t.Errorf("Message mismatch: got %q want %q", evt.Message, pokeMsg)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for poke notification")
	}
}

func TestIntegration_PokeEmptyMessage(t *testing.T) {
	addr := requireTeamSpeakAddr(t)

	sender := newConnectedIntegrationClient(t, addr, "poke-tx2", slog.Default())
	receiver := newConnectedIntegrationClient(t, addr, "poke-rx2", slog.Default())

	poked := make(chan teamspeak.PokeEvent, 1)
	receiver.OnPoked(func(e teamspeak.PokeEvent) {
		select {
		case poked <- e:
		default:
		}
	})

	time.Sleep(500 * time.Millisecond)

	if err := sender.Poke(receiver.ClientID(), ""); err != nil {
		t.Fatalf("Poke (empty): %v", err)
	}
	t.Logf("sent empty poke from clid=%d to clid=%d", sender.ClientID(), receiver.ClientID())

	select {
	case evt := <-poked:
		t.Logf("empty poke received: invoker=%q msg=%q", evt.InvokerName, evt.Message)
		if evt.Message != "" {
			t.Errorf("expected empty message, got %q", evt.Message)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for empty poke notification")
	}
}
