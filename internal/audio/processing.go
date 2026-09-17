package audio

import (
	"errors"
	"fmt"
	"math"
)

type speechProcessor interface {
	Process([]float32, []float32)
	Close()
}

type ProcessorChain struct {
	processors [2]speechProcessor
	count      int
}

type ProcessorFactory func(ProcessorSpec) (speechProcessor, error)

func BuildChain(specs []ProcessorSpec, factory ProcessorFactory) (*ProcessorChain, error) {
	chain := &ProcessorChain{}
	for _, spec := range specs {
		if spec.Backend == "" || spec.Backend == "none" {
			continue
		}
		if chain.count == len(chain.processors) {
			chain.Close()
			return nil, errors.New("too many audio processors")
		}
		p, err := factory(spec)
		if err != nil {
			chain.Close()
			return nil, fmt.Errorf("%s/%s: %w", spec.Name, spec.Backend, err)
		}
		chain.processors[chain.count] = p
		chain.count++
	}
	return chain, nil
}

func (c *ProcessorChain) Process(samples, reference []float32) {
	if c == nil {
		return
	}
	for i := 0; i < c.count; i++ {
		c.processors[i].Process(samples, reference)
	}
}

func (c *ProcessorChain) Close() {
	if c == nil {
		return
	}
	for i := 0; i < c.count; i++ {
		c.processors[i].Close()
		c.processors[i] = nil
	}
	c.count = 0
}

func validateProcessing(config ProcessingConfig) error {
	for i, name := range [...]string{"aec", "ans"} {
		if err := validateProcessor("preprocess", name, config.Preprocess[i]); err != nil {
			return err
		}
	}
	for i, name := range [...]string{"agc"} {
		if err := validateProcessor("postprocess", name, config.Postprocess[i]); err != nil {
			return err
		}
	}
	return nil
}

func validateProcessor(phase, expected string, spec ProcessorSpec) error {
	if spec == (ProcessorSpec{}) {
		return nil
	}
	if spec.Name != expected {
		return fmt.Errorf("%s 处理项顺序无效：期望 %s", phase, expected)
	}
	if spec.Backend == "none" {
		if spec.Params != (ProcessorParams{}) {
			return errors.New("已关闭的处理项不能携带参数")
		}
		return nil
	}
	p := spec.Params
	if spec.Backend == "webrtc" {
		if !AvailableWebRTC() {
			return fmt.Errorf("%s/WebRTC 处理器不可用，请安装匹配的音频库", expected)
		}
		switch expected {
		case "aec":
			if p != (ProcessorParams{}) {
				return errors.New("AEC3 不接受 SpeexDSP 参数")
			}
		case "ans":
			if p.TailMS != 0 || p.Residual || p.Target != 0 || p.MaxGainDB != 0 || p.HeadroomDB != 0 || (p.Level != 0 && (p.Level < 1 || p.Level > 4)) {
				return errors.New("WebRTC 降噪参数无效")
			}
		case "agc":
			if p.Level != 0 || p.TailMS != 0 || p.Residual || p.Target != 0 || (p.HeadroomDB != 0 && (p.HeadroomDB < 1 || p.HeadroomDB > 20)) || (p.MaxGainDB != 0 && (p.MaxGainDB < 1 || p.MaxGainDB > 24)) {
				return errors.New("WebRTC 自动增益参数无效")
			}
		}
		return nil
	}
	if spec.Backend != "speex" || !speexAvailable {
		return fmt.Errorf("%s/%s 处理器不可用", expected, spec.Backend)
	}
	switch expected {
	case "aec":
		if p.Level != 0 || p.Target != 0 || p.MaxGainDB != 0 || p.HeadroomDB != 0 || (p.TailMS != 0 && (p.TailMS < 40 || p.TailMS > 500 || p.TailMS%20 != 0)) {
			return errors.New("回声消除参数无效")
		}
	case "ans":
		if p.TailMS != 0 || p.Residual || p.Target != 0 || p.MaxGainDB != 0 || p.HeadroomDB != 0 || (p.Level != 0 && (p.Level < 1 || p.Level > 3)) {
			return errors.New("降噪参数无效")
		}
	case "agc":
		if p.Level != 0 || p.TailMS != 0 || p.Residual || p.HeadroomDB != 0 || (p.Target != 0 && (p.Target < 2048 || p.Target > 16384)) || (p.MaxGainDB != 0 && (p.MaxGainDB < 1 || p.MaxGainDB > 24)) {
			return errors.New("自动增益参数无效")
		}
	}
	return nil
}

func AvailableWebRTC() bool { return webRtcAvailable() }

func inputLevelDB(samples []float32) int {
	var energy float64
	for _, sample := range samples {
		energy += float64(sample) * float64(sample)
	}
	if energy == 0 || len(samples) == 0 {
		return -60
	}
	return max(-60, min(0, int(10*math.Log10(energy/float64(len(samples))))))
}
