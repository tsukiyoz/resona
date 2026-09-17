//go:build cgo

// Package speexdsp binds the vendored SpeexDSP 1.2.1 speech processor.
package speexdsp

/*
#cgo CFLAGS: -I${SRCDIR}/include -DFLOATING_POINT -DUSE_SMALLFT -DEXPORT=
#cgo !windows LDFLAGS: -lm
#include "speex/speex_echo.h"
#include "speex/speex_preprocess.h"
static void set_option(SpeexPreprocessState *s, int option, int value) {
  speex_preprocess_ctl(s, option, &value);
}
static void echo_rate(SpeexEchoState *s, int rate) {
  speex_echo_ctl(s, SPEEX_ECHO_SET_SAMPLING_RATE, &rate);
}
*/
import "C"

import (
	"errors"
	"unsafe"
)

type Processor struct {
	pre                      *C.SpeexPreprocessState
	echo                     *C.SpeexEchoState
	input, reference, output []int16
}

func New(frame, rate, noiseDB int, echo, residual bool) (*Processor, error) {
	if residual && !echo {
		return nil, errors.New("residual echo suppression requires echo cancellation")
	}
	p := &Processor{input: make([]int16, frame), reference: make([]int16, frame), output: make([]int16, frame)}
	p.pre = C.speex_preprocess_state_init(C.int(frame), C.int(rate))
	if p.pre == nil {
		return nil, errors.New("SpeexDSP preprocessor allocation failed")
	}
	denoise := 0
	if noiseDB < 0 {
		denoise = 1
	}
	C.set_option(p.pre, C.SPEEX_PREPROCESS_SET_DENOISE, C.int(denoise))
	C.set_option(p.pre, C.SPEEX_PREPROCESS_SET_NOISE_SUPPRESS, C.int(noiseDB))
	if echo {
		p.echo = C.speex_echo_state_init(C.int(frame), C.int(rate/5))
		if p.echo == nil {
			p.Close()
			return nil, errors.New("SpeexDSP echo allocation failed")
		}
		C.echo_rate(p.echo, C.int(rate))
		if residual {
			C.speex_preprocess_ctl(p.pre, C.SPEEX_PREPROCESS_SET_ECHO_STATE, unsafe.Pointer(p.echo))
			C.set_option(p.pre, C.SPEEX_PREPROCESS_SET_ECHO_SUPPRESS, -40)
			C.set_option(p.pre, C.SPEEX_PREPROCESS_SET_ECHO_SUPPRESS_ACTIVE, -15)
		}
	}
	return p, nil
}

func NewAEC(frame, rate, tailMS int, residual bool) (*Processor, error) {
	if tailMS == 0 {
		tailMS = 200
	}
	p := &Processor{input: make([]int16, frame), reference: make([]int16, frame), output: make([]int16, frame)}
	p.echo = C.speex_echo_state_init(C.int(frame), C.int(rate*tailMS/1000))
	if p.echo == nil {
		return nil, errors.New("SpeexDSP echo allocation failed")
	}
	C.echo_rate(p.echo, C.int(rate))
	if residual {
		p.pre = C.speex_preprocess_state_init(C.int(frame), C.int(rate))
		if p.pre == nil {
			p.Close()
			return nil, errors.New("SpeexDSP residual suppressor allocation failed")
		}
		C.set_option(p.pre, C.SPEEX_PREPROCESS_SET_DENOISE, 0)
		C.speex_preprocess_ctl(p.pre, C.SPEEX_PREPROCESS_SET_ECHO_STATE, unsafe.Pointer(p.echo))
		C.set_option(p.pre, C.SPEEX_PREPROCESS_SET_ECHO_SUPPRESS, -40)
		C.set_option(p.pre, C.SPEEX_PREPROCESS_SET_ECHO_SUPPRESS_ACTIVE, -15)
	}
	return p, nil
}

func NewANS(frame, rate, level int) (*Processor, error) {
	if level == 0 {
		level = 2
	}
	return New(frame, rate, -level*10, false, false)
}

// NewAGC owns one mono receiver's gain history. No echo or denoising is enabled.
func NewAGC(frame, rate int) (*Processor, error) {
	return NewAGCWith(frame, rate, 8192, 18)
}

func NewAGCWith(frame, rate, target, maxGainDB int) (*Processor, error) {
	if target == 0 {
		target = 8192
	}
	if maxGainDB == 0 {
		maxGainDB = 18
	}
	p, err := New(frame, rate, 0, false, false)
	if err != nil {
		return nil, err
	}
	C.set_option(p.pre, C.SPEEX_PREPROCESS_SET_AGC, 1)
	C.set_option(p.pre, C.SPEEX_PREPROCESS_SET_AGC_TARGET, C.int(target))
	C.set_option(p.pre, C.SPEEX_PREPROCESS_SET_AGC_MAX_GAIN, C.int(maxGainDB))
	C.set_option(p.pre, C.SPEEX_PREPROCESS_SET_AGC_INCREMENT, 6)
	C.set_option(p.pre, C.SPEEX_PREPROCESS_SET_AGC_DECREMENT, 40)
	return p, nil
}

// Process is called by a single worker with exactly one mono frame.
func (p *Processor) Process(samples, reference []float32) {
	for i, sample := range samples {
		p.input[i] = int16(max(-32768, min(32767, sample*32768)))
	}
	input := (*C.spx_int16_t)(unsafe.Pointer(&p.input[0]))
	if p.echo != nil {
		for i, sample := range reference {
			p.reference[i] = int16(max(-32768, min(32767, sample*32768)))
		}
		C.speex_echo_cancellation(p.echo, input, (*C.spx_int16_t)(unsafe.Pointer(&p.reference[0])), (*C.spx_int16_t)(unsafe.Pointer(&p.output[0])))
		input = (*C.spx_int16_t)(unsafe.Pointer(&p.output[0]))
	}
	if p.pre != nil {
		C.speex_preprocess_run(p.pre, input)
	}
	output := p.input
	if p.echo != nil {
		output = p.output
	}
	for i, sample := range output {
		samples[i] = float32(sample) / 32768
	}
}

func (p *Processor) Close() {
	if p.pre != nil {
		C.speex_preprocess_state_destroy(p.pre)
		p.pre = nil
	}
	if p.echo != nil {
		C.speex_echo_state_destroy(p.echo)
		p.echo = nil
	}
}
