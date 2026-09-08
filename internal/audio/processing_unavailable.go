//go:build !cgo

package audio

import "errors"

func newSpeechProcessor(config VoiceConfig) (speechProcessor, error) {
	if noiseAttenuation(config.NoiseSuppression) == 0 && !config.EchoCancellation && !config.EchoSuppression {
		return nil, nil
	}
	return nil, errors.New("音频降噪与回声消除需要启用 cgo 的构建")
}
