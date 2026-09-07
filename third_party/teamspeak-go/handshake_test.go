package teamspeak

import (
	"encoding/binary"
	"strings"
	"testing"
	"time"

	"github.com/honeybbq/teamspeak-go/commands"
	"github.com/honeybbq/teamspeak-go/crypto"
	"github.com/honeybbq/teamspeak-go/transport"
)

func TestHandleInitServer_SetsStatusConnected(t *testing.T) {
	c := newTestClient(t)

	c.handleCommand("initserver aclid=7 virtualserver_name=TestServer")

	c.mu.Lock()
	status := c.status
	clid := c.clid
	c.mu.Unlock()

	if status != StatusConnected {
		t.Errorf("expected StatusConnected, got %v", status)
	}
	if clid != 7 {
		t.Errorf("expected clid=7, got %d", clid)
	}
}

func TestHandleInitServer_FallsBackToClid(t *testing.T) {
	c := newTestClient(t)

	c.handleCommand("initserver clid=42 virtualserver_name=X")

	c.mu.Lock()
	clid := c.clid
	c.mu.Unlock()

	if clid != 42 {
		t.Errorf("expected clid=42, got %d", clid)
	}
}

func TestHandleInitServer_ClosesConnectedChan(t *testing.T) {
	c := newTestClient(t)

	c.handleCommand("initserver aclid=3 virtualserver_name=X")

	select {
	case <-c.connectedChan:
	case <-time.After(time.Second):
		t.Error("connectedChan was not closed after initserver")
	}
}

func TestHandleInitServer_TriggersOnConnected(t *testing.T) {
	c := newTestClient(t)

	connected := make(chan struct{}, 1)
	c.OnConnected(func() { connected <- struct{}{} })

	c.handleCommand("initserver aclid=1")

	select {
	case <-connected:
	case <-time.After(time.Second):
		t.Error("OnConnected not called after initserver")
	}
}

func TestHandleInitServer_CalledTwice_StillConnected(t *testing.T) {
	c := newTestClient(t)

	c.handleCommand("initserver aclid=1")
	c.handleCommand("initserver aclid=2")

	c.mu.Lock()
	clid := c.clid
	c.mu.Unlock()

	if clid != 2 {
		t.Errorf("expected clid=2 after second initserver, got %d", clid)
	}
}

// handleHandshakeInitIV (old path)

func TestHandleHandshakeInitIV_InvalidAlpha_NoSendClientInit(t *testing.T) {
	c := newTestClient(t)
	// Invalid base64 for alpha: InitCrypto fails, handleHandshakeInitIV returns.
	// Must not panic.
	c.handleCommand("clientinitiv alpha=!!! beta=AAAAAAAAAA omega=")
}

func TestHandleHandshakeInitIV_ValidParams_SendsClientInit(t *testing.T) {
	c, serverConn := newTestClientWithPipe(t)

	// Drain the initial Init1 packet from Start().
	_ = readFromPipe(t, serverConn)

	// Wire OnPacket so that the handler routes Init1 packets to handlePacket.
	c.handler.OnPacket = c.handlePacket
	c.handler.OnClosed = func(err error) {}

	// A valid old-style handshake: alpha + beta are 10-byte base64, omega is the
	// server's public key. We use the test identity's own public key as omega
	// (InitCrypto only validates format, not that it comes from a server).
	pubKey := c.crypt.Identity.PublicKeyBase64()

	cmd := "clientinitiv alpha=AAAAAAAAAA== beta=AAAAAAAAAA== omega=" + pubKey
	c.handleCommand(cmd)

	// After successful InitCrypto, sendClientInit is called and a Command packet
	// (type 2) is sent through the handler. The payload is encrypted.
	pkt := readFromPipe(t, serverConn)
	if len(pkt) < 13 {
		t.Fatalf("expected clientinit packet, got %d bytes", len(pkt))
	}
	// C2S header[4] byte (index 12 in S2C layout) = TypeFlagged; lower nibble = type.
	pktType := pkt[12] & 0x0F
	if pktType != 0x02 {
		t.Errorf("expected PacketTypeCommand (2), got %d", pktType)
	}
}

// handleHandshakeExpand2 (new path)

func TestHandleHandshakeExpand2_InvalidBeta_NoSendClientInit(t *testing.T) {
	c := newTestClient(t)
	// Invalid base64 for beta: DecodeString fails, returns early.
	c.handleCommand("initivexpand2 l= omega= proof= beta=!!!")
}

func TestBuildClientInitCommand_DefaultAuthFieldsAreEmpty(t *testing.T) {
	c := newTestClient(t)

	cmd := commands.ParseCommand(c.buildClientInitCommand())
	if cmd == nil {
		t.Fatal("expected clientinit command")
	}

	if got := cmd.Params["client_default_channel"]; got != "" {
		t.Errorf("expected empty client_default_channel, got %q", got)
	}
	if got := cmd.Params["client_default_channel_password"]; got != "" {
		t.Errorf("expected empty client_default_channel_password, got %q", got)
	}
	if got := cmd.Params["client_server_password"]; got != "" {
		t.Errorf("expected empty client_server_password, got %q", got)
	}
}

func TestBuildClientInitCommand_IncludesConfiguredHandshakeCredentials(t *testing.T) {
	id, err := crypto.IdentityFromString(testClientIdentity)
	if err != nil {
		t.Fatalf("IdentityFromString: %v", err)
	}

	c := NewClient(
		id,
		"127.0.0.1:9987",
		"Test Bot",
		WithServerPassword("server secret"),
		WithDefaultChannel("Lobby Alpha"),
		WithDefaultChannelPassword("channel secret"),
	)

	cmd := commands.ParseCommand(c.buildClientInitCommand())
	if cmd == nil {
		t.Fatal("expected clientinit command")
	}

	if got := cmd.Params["client_server_password"]; got != prepareClientPassword("server secret") {
		t.Errorf("expected client_server_password to be %q, got %q", prepareClientPassword("server secret"), got)
	}
	if got := cmd.Params["client_default_channel"]; got != "Lobby Alpha" {
		t.Errorf("expected client_default_channel to be %q, got %q", "Lobby Alpha", got)
	}
	if got := cmd.Params["client_default_channel_password"]; got != prepareClientPassword("channel secret") {
		t.Errorf(
			"expected client_default_channel_password to be %q, got %q",
			prepareClientPassword("channel secret"),
			got,
		)
	}
}

func TestBuildClientInitCommand_PreservesCredentialFieldOrder(t *testing.T) {
	id, err := crypto.IdentityFromString(testClientIdentity)
	if err != nil {
		t.Fatalf("IdentityFromString: %v", err)
	}

	c := NewClient(
		id,
		"127.0.0.1:9987",
		"Test Bot",
		WithServerPassword("server secret"),
		WithDefaultChannel("Lobby Alpha"),
		WithDefaultChannelPassword("channel secret"),
	)

	raw := c.buildClientInitCommand()
	defaultChannelIndex := indexOfOrFail(t, raw, "client_default_channel=Lobby\\sAlpha")
	defaultChannelPasswordIndex := indexOfOrFail(
		t,
		raw,
		"client_default_channel_password="+commands.Escape(prepareClientPassword("channel secret")),
	)
	serverPasswordIndex := indexOfOrFail(
		t,
		raw,
		"client_server_password="+commands.Escape(prepareClientPassword("server secret")),
	)
	metaDataIndex := indexOfOrFail(t, raw, "client_meta_data=")

	if defaultChannelIndex >= defaultChannelPasswordIndex ||
		defaultChannelPasswordIndex >= serverPasswordIndex ||
		serverPasswordIndex >= metaDataIndex {
		t.Fatalf("unexpected credential field order in %q", raw)
	}
}

func TestHandlePacket_Init1_Step0_SendsStep1(t *testing.T) {
	c, serverConn := newTestClientWithPipe(t)
	c.handler.OnPacket = c.handlePacket
	c.handler.OnClosed = func(error) {}

	// Drain initial C2S Init1 from Start().
	_ = readFromPipe(t, serverConn)

	// Build step-0 server response: data[0]=0x00
	step0 := make([]byte, 21)
	step0[0] = 0x00
	binary.LittleEndian.PutUint32(step0[9:13], 0xCAFEBABE)

	// Wrap as S2C Init1 raw packet: [8 tag][2 ID][1 TypeFlagged][21 payload]
	pktBytes := make([]byte, 8+3+len(step0))
	pktBytes[10] = 0x08 // PacketTypeInit1
	copy(pktBytes[11:], step0)
	_, writeErr := serverConn.Write(pktBytes)
	if writeErr != nil {
		t.Fatalf("Write: %v", writeErr)
	}

	// handlePacket calls handler.SendPacket(8, step1Response, 0),
	// which writes through the pipe.
	resp := readFromPipe(t, serverConn)
	if len(resp) < 13 {
		t.Fatalf("expected step-1 response, got %d bytes", len(resp))
	}
	if resp[12]&0x0F != 0x08 {
		t.Errorf("expected PacketTypeInit1 (8), got %d", resp[12]&0x0F)
	}
	// Step-1 payload starts with 0x01
	if resp[13] != 0x01 {
		t.Errorf("expected step 0x01 payload, got 0x%02x", resp[13])
	}
}

func TestHandlePacket_CommandType_RoutedToHandleCommandLines(t *testing.T) {
	c := newTestClient(t)
	entered := make(chan struct{}, 1)
	c.OnClientEnter(func(_ ClientInfo) { entered <- struct{}{} })
	c.rebuildMiddlewareChains()

	enterLine := "notifycliententerview clid=9 client_nickname=G" +
		" ctid=1 client_type=0 client_servergroups= client_unique_identifier=g"
	p := &transport.Packet{
		TypeFlagged: 0x02, // PacketTypeCommand
		Data:        []byte(enterLine),
	}
	c.handlePacket(p)

	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Error("handlePacket did not route Command to handleCommandLines")
	}
}

// readFromPipe reads one datagram from the server-side pipe with a 2s timeout.
func readFromPipe(t *testing.T, server *pipePair) []byte {
	t.Helper()
	buf := make([]byte, 4096)
	done := make(chan []byte, 1)
	go func() {
		n, readErr := server.Read(buf)
		if readErr != nil {
			done <- nil

			return
		}
		cp := make([]byte, n)
		copy(cp, buf[:n])
		done <- cp
	}()
	select {
	case data := <-done:
		return data
	case <-time.After(2 * time.Second):
		t.Fatal("readFromPipe: timed out")

		return nil
	}
}

func indexOfOrFail(t *testing.T, s string, needle string) int {
	t.Helper()

	idx := strings.Index(s, needle)
	if idx < 0 {
		t.Fatalf("expected %q to contain %q", s, needle)
	}

	return idx
}
