// Package audio owns Resona's real-time voice pipeline independently of the GUI.
package audio

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const (
	MaxPlaybackVolume = 794 // +18 dB, as a linear percentage; UI uses dB.
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
	Instance   string    `json:"-"`
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

// ChannelBitrateSource returns the authoritative target bitrate without blocking.
// Implementations must support reads concurrent with control-state updates.
type ChannelBitrateSource interface{ VoiceBitrate() int }

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

type ProcessorParams struct {
	Level      int  `json:"level,omitempty"`
	TailMS     int  `json:"tailMs,omitempty"`
	Residual   bool `json:"residual,omitempty"`
	Target     int  `json:"target,omitempty"`
	MaxGainDB  int  `json:"maxGainDb,omitempty"`
	HeadroomDB int  `json:"headroomDb,omitempty"`
}

type ProcessorSpec struct {
	Name    string          `json:"name"`
	Backend string          `json:"backend"`
	Params  ProcessorParams `json:"params"`
}

// Fixed slots preserve the validated execution order and keep VoiceConfig comparable.
type ProcessingConfig struct {
	Preprocess  [2]ProcessorSpec `json:"preprocess"`
	Postprocess [1]ProcessorSpec `json:"postprocess"`
}

type VoiceConfig struct {
	// AutoUnmuteOnConnect applies once on an explicit connection, never on reconnect.
	AutoUnmuteOnConnect bool `json:"autoUnmuteOnConnect,omitempty"`
	// LocalMonitor is controlled by the service, never deserialized from GUI preferences.
	LocalMonitor   bool   `json:"-"`
	Enabled        bool   `json:"enabled"`
	Muted          bool   `json:"muted"`
	Deafened       bool   `json:"deafened"`
	InputDeviceID  string `json:"inputDeviceID"`
	OutputDeviceID string `json:"outputDeviceID"`
	Volume         int    `json:"volume"`
	// InputGain is explicit: 0 silences input; constructors and IPC default to 100.
	InputGain      int              `json:"inputGain"`
	ActivationMode string           `json:"activationMode"`
	VADThresholdDB int              `json:"vadThresholdDB"`
	Processing     ProcessingConfig `json:"processing"`
	Ducking        bool             `json:"ducking"`
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
	if err := validateProcessing(config.Processing); err != nil {
		return err
	}
	if config.InputGain < 0 || config.InputGain > 200 {
		return errors.New("麦克风输入增益必须在 0 到 200 之间")
	}
	if config.Volume < 0 || config.Volume > MaxPlaybackVolume {
		return errors.New("收听增益超出范围")
	}
	if config.ActivationMode != "" && config.ActivationMode != "continuous" && config.ActivationMode != "ptt" && config.ActivationMode != "vad" {
		return errors.New("未知的语音激活模式")
	}
	if config.VADThresholdDB < -60 || config.VADThresholdDB > 0 {
		return errors.New("语音阈值必须在 -60 到 0 dB 之间")
	}
	return nil
}

func ValidateConfig(config VoiceConfig) error { return validateConfig(config) }
