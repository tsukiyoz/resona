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
	Enabled          bool   `json:"enabled"`
	Muted            bool   `json:"muted"`
	Deafened         bool   `json:"deafened"`
	InputDeviceID    string `json:"inputDeviceID"`
	OutputDeviceID   string `json:"outputDeviceID"`
	Volume           int    `json:"volume"`
	ActivationMode   string `json:"activationMode"`
	VADThresholdDB   int    `json:"vadThresholdDB"`
	NoiseSuppression string `json:"noiseSuppression"`
	EchoCancellation bool   `json:"echoCancellation"`
	EchoSuppression  bool   `json:"echoSuppression"`
	Ducking          bool   `json:"ducking"`
}

type VoiceState struct {
	Config            VoiceConfig `json:"config"`
	Active            bool        `json:"active"`
	ChannelCodec      Codec       `json:"channelCodec"`
	Error             string      `json:"error"`
	SpeakingClientIDs []uint16    `json:"speakingClientIDs"`
	LocalSpeaking     bool        `json:"localSpeaking"`
	PushToTalkPressed bool        `json:"pushToTalkPressed"`
	InputLevelDB      int         `json:"inputLevelDB"`
}

var (
	ErrClosed           = errors.New("语音引擎已关闭")
	ErrUnsupportedCodec = errors.New("当前频道的语音编码不受支持")
)

func validateConfig(config VoiceConfig) error {
	if config.Volume < 0 || config.Volume > 100 {
		return errors.New("音量必须在 0 到 100 之间")
	}
	if config.ActivationMode != "" && config.ActivationMode != "continuous" && config.ActivationMode != "ptt" && config.ActivationMode != "vad" {
		return errors.New("未知的语音激活模式")
	}
	if config.VADThresholdDB < -60 || config.VADThresholdDB > 0 {
		return errors.New("语音阈值必须在 -60 到 0 dB 之间")
	}
	if config.NoiseSuppression != "" && config.NoiseSuppression != "off" && config.NoiseSuppression != "low" && config.NoiseSuppression != "medium" && config.NoiseSuppression != "high" {
		return errors.New("未知的降噪级别")
	}
	return nil
}

func ValidateConfig(config VoiceConfig) error { return validateConfig(config) }
