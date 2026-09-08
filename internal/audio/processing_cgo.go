//go:build cgo

package audio

import "github.com/tsukiyoz/resona/internal/audio/speexdsp"

func newSpeechProcessor(config VoiceConfig) (speechProcessor, error) {
	if noiseAttenuation(config.NoiseSuppression) == 0 && !config.EchoCancellation && !config.EchoSuppression {
		return nil, nil
	}
	return speexdsp.New(FrameSamples, SampleRate, noiseAttenuation(config.NoiseSuppression), config.EchoCancellation, config.EchoSuppression)
}
