package audio

import "context"

// NewMicrophoneTest creates an isolated local monitor. It has no network
// transport and cannot send captured audio to a connected session.
func NewMicrophoneTest(notify func(VoiceState)) *Engine {
	engine := New(monitorTransport{}, notify)
	engine.monitor = true
	return engine
}

type monitorTransport struct{}

func (monitorTransport) SetVoiceHandler(func(Packet)) {}
func (monitorTransport) SendVoice([]byte, Codec) error {
	panic("local microphone monitor cannot transmit")
}
func (monitorTransport) VoiceCodec() (Codec, error)                      { return CodecOpusVoice, nil }
func (monitorTransport) SetVoiceMuted(context.Context, bool, bool) error { return nil }
