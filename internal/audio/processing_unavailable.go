//go:build !cgo

package audio

import "errors"

const speexAvailable = false

func webRtcAvailable() bool { return false }

func newPlaybackProcessor() (speechProcessor, error) {
	return nil, errors.New("收听自动增益需要启用 cgo 的构建")
}

func newSpeechProcessor(config VoiceConfig) (speechProcessor, error) {
	return buildUnavailableChain(config.Processing.Preprocess[:])
}

func newReceiveProcessor(config ProcessingConfig) (speechProcessor, error) {
	return buildUnavailableChain(config.Postprocess[:])
}

func buildUnavailableChain(specs []ProcessorSpec) (speechProcessor, error) {
	for _, spec := range specs {
		if spec.Backend != "" && spec.Backend != "none" {
			return nil, errors.New("音频处理需要启用 cgo 的构建")
		}
	}
	return nil, nil
}
