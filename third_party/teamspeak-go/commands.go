package teamspeak

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/honeybbq/teamspeak-go/commands"
	"github.com/honeybbq/teamspeak-go/handshake"
	"github.com/honeybbq/teamspeak-go/transport"
)

const maxVoiceDataBytes = 1275

var (
	errTeamSpeakCommand = errors.New("TeamSpeak server error")
	errCommandTimed     = errors.New("command timeout")
)

// CommandError is a nonzero error response returned by the TeamSpeak server.
type CommandError struct {
	ID      uint32
	Message string
}

func (e *CommandError) Error() string {
	return fmt.Sprintf("%s: %s (id=%d)", errTeamSpeakCommand, e.Message, e.ID)
}

func (e *CommandError) Unwrap() error { return errTeamSpeakCommand }

type commandResult struct {
	Err  error
	Data []map[string]string
}

// commandTracker matches return_code values to pending commands and response rows.
type commandTracker struct {
	pending    map[uint32]chan commandResult
	collecting map[uint32][]map[string]string
	mu         sync.Mutex
	nextRC     uint32
}

func newCommandTracker() *commandTracker {
	return &commandTracker{
		pending:    make(map[uint32]chan commandResult),
		collecting: make(map[uint32][]map[string]string),
	}
}

func (t *commandTracker) register() (uint32, <-chan commandResult) {
	rc := atomic.AddUint32(&t.nextRC, 1)
	ch := make(chan commandResult, 1)
	t.mu.Lock()
	t.pending[rc] = ch
	t.mu.Unlock()

	return rc, ch
}

func (t *commandTracker) unregister(rc uint32) {
	t.mu.Lock()
	delete(t.pending, rc)
	delete(t.collecting, rc)
	t.mu.Unlock()
}

// collect appends a parameter row to the pending command with the largest return_code.
func (t *commandTracker) collect(params map[string]string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	var maxRC uint32
	for rc := range t.pending {
		if rc > maxRC {
			maxRC = rc
		}
	}
	if maxRC > 0 {
		t.collecting[maxRC] = append(t.collecting[maxRC], params)
	}
}

func (t *commandTracker) resolve(rc uint32, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if ch, ok := t.pending[rc]; ok {
		data := t.collecting[rc]
		delete(t.pending, rc)
		delete(t.collecting, rc)
		ch <- commandResult{Data: data, Err: err}
	}
}

func (t *commandTracker) reset() {
	t.mu.Lock()
	t.pending = make(map[uint32]chan commandResult)
	t.collecting = make(map[uint32][]map[string]string)
	t.mu.Unlock()
}

func (c *Client) handlePacket(p *transport.Packet) {
	c.logger.Debug("received packet", slog.Uint64("type", uint64(p.Type())), slog.Int("length", len(p.Data)))
	switch p.Type() {
	case transport.PacketTypeInit1:
		c.logger.Debug("processing init1 packet")
		response := handshake.ProcessInit1(c.crypt, p.Data)
		if response != nil {
			c.logger.Debug("sending init1 response")
			err := c.handler.SendPacket(byte(transport.PacketTypeInit1), response, 0)
			if err != nil {
				c.logger.Warn("failed to send init1 response", slog.Any("error", err))
			}
		}
	case transport.PacketTypeCommand, transport.PacketTypeCommandLow:
		if len(p.Data) == 0 {
			return
		}
		dataStr := string(p.Data)
		c.logger.Debug("received command data", slog.String("data", dataStr))
		c.handleCommandLines(dataStr)
	case transport.PacketTypeVoice, transport.PacketTypeVoiceWhisper:
		c.handleVoicePacket(p)
	case transport.PacketTypePing, transport.PacketTypePong, transport.PacketTypeAck, transport.PacketTypeAckLow:
		return
	}
}

func (c *Client) handleVoicePacket(p *transport.Packet) {
	// Server voice payload: voice sequence, sender client ID, codec, Opus data.
	if len(p.Data) < 5 || len(p.Data)-5 > maxVoiceDataBytes {
		c.logger.Warn("dropping malformed voice packet", slog.Int("length", len(p.Data)))
		return
	}
	packet := VoicePacket{
		ReceivedAt: p.ReceivedAt,
		Sequence:   binary.BigEndian.Uint16(p.Data[0:2]),
		SenderID:   binary.BigEndian.Uint16(p.Data[2:4]),
		Codec:      p.Data[4],
		Data:       append([]byte(nil), p.Data[5:]...),
		Whisper:    p.Type() == transport.PacketTypeVoiceWhisper,
		Encrypted:  !p.IsUnencrypted(),
		End:        len(p.Data) == 5,
	}
	for _, observer := range c.voiceObservers {
		observed := packet
		observed.Data = append([]byte(nil), packet.Data...)
		observer(observed)
	}
}

func (c *Client) handleCommandLines(s string) {
	if s == "" {
		return
	}
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == '\n' || s[i] == 0x00 {
			part := strings.TrimSuffix(s[start:i], "\r")
			if part != "" {
				rows := splitCommandRows(part)
				for _, row := range rows {
					c.handleCommand(row)
				}
			}
			start = i + 1
		}
	}
}

func (c *Client) handleCommand(s string) {
	cmd := commands.ParseCommand(s)
	if cmd == nil || cmd.Name == "" {
		return
	}
	for _, observer := range c.commandObservers {
		observer(IncomingCommand{Name: cmd.Name, Params: maps.Clone(cmd.Params)})
	}

	c.logger.Debug("processing command", slog.String("name", cmd.Name), slog.Any("params", cmd.Params))

	if strings.HasPrefix(cmd.Name, "notify") {
		c.handleNotification(cmd)

		return
	}

	switch cmd.Name {
	case "clientinitiv":
		c.handleHandshakeInitIV(cmd)
	case "initivexpand2":
		c.handleHandshakeExpand2(cmd)
	case "initserver":
		c.handleInitServer(cmd)
	case "error":
		c.handleError(cmd)
	default:
		c.cmdTrack.collect(cmd.Params)
		c.logger.Debug("unhandled or data command", slog.String("name", cmd.Name), slog.Any("params", cmd.Params))
	}
}

func (c *Client) handleError(cmd *commands.Command) {
	id := cmd.Params["id"]
	msg := cmd.Params["msg"]
	rcStr := cmd.Params["return_code"]

	var err error
	if id != "0" {
		if number, parseErr := strconv.ParseUint(id, 10, 32); parseErr == nil {
			err = &CommandError{ID: uint32(number), Message: msg}
		} else {
			err = fmt.Errorf("%w: %s (id=%s)", errTeamSpeakCommand, msg, id)
		}
		c.logger.Error("server returned error", slog.String("id", id), slog.String("message", msg))

		if id == "3329" {
			c.logger.Warn("fatal connection error detected, closing connection", slog.String("id", id))
			go func() {
				disconnectErr := c.Disconnect()
				if disconnectErr != nil {
					c.logger.Warn("disconnect after fatal error failed", slog.Any("error", disconnectErr))
				}
			}()
		}
	}

	if rcStr != "" {
		rc, parseErr := strconv.ParseUint(rcStr, 10, 32)
		if parseErr == nil {
			c.cmdTrack.resolve(uint32(rc), err)
		}
	}
}

// SendCommandNoWait sends a command without waiting for return_code.
func (c *Client) SendCommandNoWait(cmd string) error {
	err := c.throttle.wait(context.Background())
	if err != nil {
		return err
	}
	c.logger.Debug("sending command without waiting", slog.String("raw", cmd))

	return c.finalCmdHandler(cmd)
}

// ExecCommand sends a command and waits for its return_code response.
func (c *Client) ExecCommand(cmd string, timeout time.Duration) error {
	_, err := c.ExecCommandWithResponse(cmd, timeout)

	return err
}

// ExecCommandContext waits for throttling and the server response until ctx ends.
// Cancellation cannot retract a command that has already been sent.
func (c *Client) ExecCommandContext(ctx context.Context, cmd string) error {
	_, err := c.execCommandWithResponse(ctx, cmd)
	return err
}

// ExecCommandWithResponseContext waits for response rows and command completion
// until ctx ends. Cancellation cannot retract a command already sent.
func (c *Client) ExecCommandWithResponseContext(ctx context.Context, cmd string) ([]map[string]string, error) {
	return c.execCommandWithResponse(ctx, cmd)
}

// ExecCommandWithResponse sends a command and waits for its return_code response and data.
func (c *Client) ExecCommandWithResponse(cmd string, timeout time.Duration) ([]map[string]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	data, err := c.execCommandWithResponse(ctx, cmd)
	if errors.Is(err, context.DeadlineExceeded) {
		return nil, fmt.Errorf("%w: %s", errCommandTimed, cmd)
	}
	return data, err
}

func (c *Client) execCommandWithResponse(ctx context.Context, cmd string) ([]map[string]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	parsed := commands.ParseCommand(cmd)
	if parsed == nil {
		return nil, errors.New("invalid command")
	}
	// The tracker owns the top-level response ID. Text parameter values may
	// contain this spelling and must not affect response correlation.
	if _, exists := parsed.Params["return_code"]; exists {
		return nil, errors.New("return_code is managed by ExecCommand")
	}
	if err := c.throttle.wait(ctx); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	rc, ch := c.cmdTrack.register()
	defer c.cmdTrack.unregister(rc)

	withReturnCode := fmt.Sprintf("%s return_code=%d", cmd, rc)

	c.logger.Debug("sending command", slog.String("raw", withReturnCode))

	err := c.finalCmdHandler(withReturnCode)
	if err != nil {
		return nil, err
	}

	select {
	case res := <-ch:
		return res.Data, res.Err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
