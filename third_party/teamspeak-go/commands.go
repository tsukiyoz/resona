package teamspeak

import (
	"context"
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

var (
	errTeamSpeakCommand = errors.New("TeamSpeak server error")
	errCommandTimed     = errors.New("command timeout")
)

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
	case transport.PacketTypeVoice, transport.PacketTypeVoiceWhisper, transport.PacketTypePing,
		transport.PacketTypePong, transport.PacketTypeAck, transport.PacketTypeAckLow:
		return
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
		err = fmt.Errorf("%w: %s (id=%s)", errTeamSpeakCommand, msg, id)
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

// ExecCommandWithResponse sends a command and waits for its return_code response and data.
func (c *Client) ExecCommandWithResponse(cmd string, timeout time.Duration) ([]map[string]string, error) {
	rc, ch := c.cmdTrack.register()
	defer c.cmdTrack.unregister(rc)

	withReturnCode := cmd
	if !strings.Contains(cmd, "return_code=") {
		withReturnCode = fmt.Sprintf("%s return_code=%d", cmd, rc)
	}

	c.logger.Debug("sending command", slog.String("raw", withReturnCode))

	err := c.throttle.wait(context.Background())
	if err != nil {
		return nil, err
	}

	err = c.finalCmdHandler(withReturnCode)
	if err != nil {
		return nil, err
	}

	select {
	case res := <-ch:
		return res.Data, res.Err
	case <-time.After(timeout):
		return nil, fmt.Errorf("%w: %s", errCommandTimed, cmd)
	}
}
