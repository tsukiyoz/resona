// Package audio owns Resona's real-time voice pipeline independently of the GUI.
package audio

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const (
	SampleRate        = 48000
	FrameSamples      = 960
	MaxOpusPacketSize = 1275
)

type Codec uint8

const (
	CodecOpusVoice Codec = 4
	CodecOpusMusic Codec = 5
)

func (c Codec) Supported() bool { return c == CodecOpusVoice || c == CodecOpusMusic }

func (c Codec) String() string {
	switch c {
	case CodecOpusVoice:
		return "opusVoice"
	case CodecOpusMusic:
		return "opusMusic"
	default:
		return fmt.Sprintf("unsupported(%d)", c)
	}
}

type Packet struct {
	ReceivedAt time.Time `json:"receivedAt"`
	Data       []byte    `json:"-"`
	Sequence   uint16    `json:"sequence"`
	SenderID   uint16    `json:"senderID"`
	Codec      Codec     `json:"codec"`
	End        bool      `json:"end"`
	invalid    bool
}

// Transport is implemented by a connected protocol adapter. SetVoiceHandler
// must accept nil and must never call an old handler after it returns.
type Transport interface {
	SetVoiceHandler(func(Packet))
	SendVoice([]byte, Codec) error
	VoiceCodec() (Codec, error)
	SetVoiceMuted(context.Context, bool, bool) error
}

type DeviceKind string

const (
	DeviceInput  DeviceKind = "input"
	DeviceOutput DeviceKind = "output"
)

type Device struct {
	ID      string     `json:"id"`
	Name    string     `json:"name"`
	Kind    DeviceKind `json:"kind"`
	Default bool       `json:"default"`
}

type VoiceConfig struct {
	Enabled        bool   `json:"enabled"`
	Muted          bool   `json:"muted"`
	Deafened       bool   `json:"deafened"`
	InputDeviceID  string `json:"inputDeviceID"`
	OutputDeviceID string `json:"outputDeviceID"`
	Volume         int    `json:"volume"`
}

type VoiceState struct {
	Config            VoiceConfig `json:"config"`
	Active            bool        `json:"active"`
	ChannelCodec      Codec       `json:"channelCodec"`
	Error             string      `json:"error"`
	SpeakingClientIDs []uint16    `json:"speakingClientIDs"`
	LocalSpeaking     bool        `json:"localSpeaking"`
}

var (
	ErrClosed           = errors.New("语音引擎已关闭")
	ErrUnsupportedCodec = errors.New("当前频道的语音编码不受支持")
)

func validateConfig(config VoiceConfig) error {
	if config.Volume < 0 || config.Volume > 100 {
		return errors.New("音量必须在 0 到 100 之间")
	}
	return nil
}
