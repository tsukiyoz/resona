//go:build cgo

package audio

import (
	"fmt"
	"log/slog"

	"github.com/tsukiyoz/resona/internal/audio/speexdsp"
	"github.com/tsukiyoz/resona/internal/audio/webrtc"
)

const speexAvailable = true

func webRtcAvailable() bool { return webrtc.Available() }

func newPlaybackProcessor() (speechProcessor, error) {
	return speexdsp.NewAGC(FrameSamples, SampleRate)
}

func newSpeechProcessor(config VoiceConfig) (speechProcessor, error) {
	chain, err := BuildChain(config.Processing.Preprocess[:], newProcessor)
	if err != nil || chain.count == 0 {
		return nil, err
	}
	return chain, nil
}

func newReceiveProcessor(config ProcessingConfig) (speechProcessor, error) {
	chain, err := BuildChain(config.Postprocess[:], newProcessor)
	if err != nil || chain.count == 0 {
		return nil, err
	}
	return chain, nil
}

type webRTCProcessor struct {
	stage  *webrtc.Stage
	failed bool
}

func (p *webRTCProcessor) Process(samples, reference []float32) {
	if p.failed {
		return
	}
	if code := p.stage.Process(samples, reference); code != 0 && !p.failed {
		p.failed = true
		slog.Warn("WebRTC 音频处理失败", "code", code)
	}
}

func (p *webRTCProcessor) Close() { p.stage.Close() }

func newProcessor(spec ProcessorSpec) (speechProcessor, error) {
	if spec.Backend == "webrtc" {
		kind := uint32(1)
		switch spec.Name {
		case "ans":
			kind = 2
		case "agc":
			kind = 3
		case "aec":
		default:
			return nil, fmt.Errorf("unsupported audio processor %q", spec.Name)
		}
		level, headroom, gain := spec.Params.Level, spec.Params.HeadroomDB, spec.Params.MaxGainDB
		if kind == 2 && level == 0 {
			level = 2
		}
		if kind == 3 {
			if headroom == 0 {
				headroom = 5
			}
			if gain == 0 {
				gain = 18
			}
		}
		stage, err := webrtc.New(kind, level, headroom, gain)
		if err != nil {
			return nil, err
		}
		return &webRTCProcessor{stage: stage}, nil
	}
	if spec.Backend != "speex" {
		return nil, fmt.Errorf("unsupported audio backend %q", spec.Backend)
	}
	switch spec.Name {
	case "aec":
		return speexdsp.NewAEC(FrameSamples, SampleRate, spec.Params.TailMS, spec.Params.Residual)
	case "ans":
		return speexdsp.NewANS(FrameSamples, SampleRate, spec.Params.Level)
	case "agc":
		return speexdsp.NewAGCWith(FrameSamples, SampleRate, spec.Params.Target, spec.Params.MaxGainDB)
	default:
		return nil, fmt.Errorf("unsupported audio processor %q", spec.Name)
	}
}
