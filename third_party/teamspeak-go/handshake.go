package teamspeak

import (
	"crypto/sha1" //nolint:gosec // SHA-1 used for TS3 HWID/UID format, not security
	"encoding/base64"
	"log/slog"
	"strconv"

	"github.com/honeybbq/teamspeak-go/commands"
	"github.com/honeybbq/teamspeak-go/crypto"
	"github.com/honeybbq/teamspeak-go/handshake"
	"github.com/honeybbq/teamspeak-go/transport"
)

func (c *Client) handleHandshakeInitIV(cmd *commands.Command) {
	c.logger.Info("received crypto negotiation")
	alpha := cmd.Params["alpha"]
	beta := cmd.Params["beta"]
	omega := cmd.Params["omega"]

	err := c.crypt.InitCrypto(alpha, beta, omega)
	if err != nil {
		c.logger.Error("failed to initialize crypto", slog.Any("error", err))

		return
	}

	c.logger.Info("crypto initialized, sending clientinit")
	c.sendClientInit()
}

func (c *Client) handleHandshakeExpand2(cmd *commands.Command) {
	c.logger.Info("received initivexpand2")
	c.handler.ReceivedFinalInitAck()
	license := cmd.Params["l"]
	omega := cmd.Params["omega"]
	proof := cmd.Params["proof"]
	beta := cmd.Params["beta"]

	privateKey, err := c.sendClientEkPacket(beta)
	if err != nil {
		c.logger.Warn("failed to send clientek", slog.Any("error", err))

		return
	}

	err = handshake.CryptoInit2(c.crypt, license, omega, proof, beta, privateKey)
	if err != nil {
		c.logger.Error("crypto init2 failed", slog.Any("error", err))

		return
	}
	c.sendClientInit()
}

func (c *Client) sendClientEkPacket(beta string) ([]byte, error) {
	publicKey, privateKey, err := crypto.GenerateTemporaryKey()
	if err != nil {
		c.logger.Error("failed to generate temporary key", slog.Any("error", err))

		return nil, err
	}
	ekBase64 := base64.StdEncoding.EncodeToString(publicKey)

	clientProof, err := c.buildClientEkProof(publicKey, beta)
	if err != nil {
		return nil, err
	}
	clientEk := commands.BuildCommandOrdered("clientek", [][2]string{
		{"ek", ekBase64},
		{"proof", clientProof},
	})
	c.logger.Debug("sending clientek", slog.String("ek", ekBase64))
	err = c.handler.SendPacket(byte(transport.PacketTypeCommand), []byte(clientEk), 0)
	if err != nil {
		return nil, err
	}

	return privateKey, nil
}

func (c *Client) buildClientEkProof(publicKey []byte, beta string) (string, error) {
	betaBytes, err := base64.StdEncoding.DecodeString(beta)
	if err != nil {
		c.logger.Error("failed to decode beta", slog.Any("error", err))

		return "", err
	}
	toSign := make([]byte, 86)
	copy(toSign, publicKey)
	if len(betaBytes) > 54 {
		betaBytes = betaBytes[:54]
	}
	copy(toSign[32:], betaBytes)
	sign, err := crypto.Sign(c.crypt.Identity.PrivateKey, toSign)
	if err != nil {
		c.logger.Error("failed to sign clientek proof", slog.Any("error", err))

		return "", err
	}

	return base64.StdEncoding.EncodeToString(sign), nil
}

func (c *Client) handleInitServer(cmd *commands.Command) {
	c.mu.Lock()
	c.status = StatusConnected

	idStr := ""
	if v, ok := cmd.Params["aclid"]; ok {
		idStr = v
	} else if v, ok := cmd.Params["clid"]; ok {
		idStr = v
	}

	if idStr != "" {
		val, err := strconv.ParseUint(idStr, 10, 16)
		if err == nil {
			c.clid = uint16(val)
			c.handler.SetClientID(c.clid)
		}
	}
	handlers := c.connectedHandlers
	c.mu.Unlock()

	c.logger.Info("connected to server", slog.Uint64("self_id", uint64(c.clid)))

	select {
	case <-c.connectedChan:
	default:
		close(c.connectedChan)
	}

	go func() {
		updateCmd := commands.BuildCommand("clientupdate", map[string]string{
			"client_input_muted":  "0",
			"client_output_muted": "0",
		})
		_ = c.SendCommandNoWait(updateCmd)
	}()

	for _, h := range handlers {
		go h()
	}
}

func (c *Client) sendClientInit() {
	cmd := c.buildClientInitCommand()
	err := c.handler.SendPacket(byte(transport.PacketTypeCommand), []byte(cmd), 0)
	if err != nil {
		c.logger.Warn("failed to send clientinit", slog.Any("error", err))
	}
}

func (c *Client) buildClientInitCommand() string {
	pubKeyBase64 := c.crypt.Identity.PublicKeyBase64()
	// HWID matches TS3 client UID format: base64(SHA1(publicKeyBase64))
	hwidSum := sha1.Sum([]byte(pubKeyBase64)) //nolint:gosec
	hwid := base64.StdEncoding.EncodeToString(hwidSum[:])
	defaultChannelPassword := prepareClientPassword(c.clientInitOptions.defaultChannelPassword)
	serverPassword := prepareClientPassword(c.clientInitOptions.serverPassword)

	return commands.BuildCommandOrdered("clientinit", [][2]string{
		{"client_nickname", c.nickname},
		{"client_version", "3.?.? [Build: 5680278000]"},
		{"client_platform", "Windows"},
		{"client_input_hardware", "1"},
		{"client_output_hardware", "1"},
		{"client_default_channel", c.clientInitOptions.defaultChannel},
		{"client_default_channel_password", defaultChannelPassword},
		{"client_server_password", serverPassword},
		{"client_meta_data", ""},
		{"client_version_sign", "DX5NIYLvfJEUjuIbCidnoeozxIDRRkpq3I9vVMBmE9L2qnekOoBzSenkzsg2lC9CMv8K5hkEzhr2TYUYSwUXCg=="},
		{"client_key_offset", strconv.FormatUint(c.crypt.Identity.Offset, 10)},
		{"client_nickname_phonetic", ""},
		{"client_default_token", ""},
		{"hwid", hwid},
	})
}

func prepareClientPassword(password string) string {
	if password == "" {
		return ""
	}

	sum := sha1.Sum([]byte(password)) //nolint:gosec // TeamSpeak protocol requires base64(sha1(password))

	return base64.StdEncoding.EncodeToString(sum[:])
}
