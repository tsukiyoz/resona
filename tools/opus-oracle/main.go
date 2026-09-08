//go:build opus_oracle

package main

/*
#cgo pkg-config: opus
#include <opus.h>
*/
import "C"

import (
	"fmt"
	"math"
	"unsafe"

	"github.com/thesyncim/gopus"
)

const sampleRate = 48000
const frameSamples = 960

func signal(channels, frame int) []float32 {
	pcm := make([]float32, frameSamples*channels)
	for i := 0; i < frameSamples; i++ {
		for ch := 0; ch < channels; ch++ {
			pcm[i*channels+ch] = float32(math.Sin(2*math.Pi*(330+float64(ch)*220)*float64(frame*frameSamples+i)/sampleRate)) * 0.25
		}
	}
	return pcm
}

func energy(pcm []float32) float64 {
	var sum float64
	for _, sample := range pcm {
		sum += math.Abs(float64(sample))
	}
	return sum
}

func probe(channels int, app gopus.Application) error {
	genc, err := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: sampleRate, Channels: channels, Application: app})
	if err != nil {
		return err
	}
	var cerror C.int
	cdec := C.opus_decoder_create(sampleRate, C.int(channels), &cerror)
	if cdec == nil || cerror != C.OPUS_OK {
		return fmt.Errorf("libopus decoder create: %d", cerror)
	}
	defer C.opus_decoder_destroy(cdec)
	packet := make([]byte, 1275)
	out := make([]float32, frameSamples*channels)
	var decodedEnergy float64
	for frame := 0; frame < 4; frame++ {
		n, encodeErr := genc.Encode(signal(channels, frame), packet)
		if encodeErr != nil {
			return encodeErr
		}
		got := C.opus_decode_float(cdec, (*C.uchar)(unsafe.Pointer(&packet[0])), C.opus_int32(n), (*C.float)(unsafe.Pointer(&out[0])), frameSamples, 0)
		if got != frameSamples {
			return fmt.Errorf("libopus decoded %d", got)
		}
		decodedEnergy += energy(out)
	}
	if decodedEnergy == 0 {
		return fmt.Errorf("libopus decoded silence")
	}

	capp := C.int(C.OPUS_APPLICATION_AUDIO)
	if channels == 1 {
		capp = C.OPUS_APPLICATION_VOIP
	}
	cenc := C.opus_encoder_create(sampleRate, C.int(channels), capp, &cerror)
	if cenc == nil || cerror != C.OPUS_OK {
		return fmt.Errorf("libopus encoder create: %d", cerror)
	}
	defer C.opus_encoder_destroy(cenc)
	gdec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		return err
	}
	decodedEnergy = 0
	for frame := 0; frame < 4; frame++ {
		pcm := signal(channels, frame)
		n := C.opus_encode_float(cenc, (*C.float)(unsafe.Pointer(&pcm[0])), frameSamples, (*C.uchar)(unsafe.Pointer(&packet[0])), C.opus_int32(len(packet)))
		if n < 0 {
			return fmt.Errorf("libopus encode: %d", n)
		}
		got, decodeErr := gdec.Decode(packet[:n], out)
		if decodeErr != nil || got != frameSamples {
			return fmt.Errorf("gopus decode=%d: %v", got, decodeErr)
		}
		decodedEnergy += energy(out)
	}
	if decodedEnergy == 0 {
		return fmt.Errorf("gopus decoded silence")
	}
	return nil
}

func main() {
	for _, probeCase := range []struct {
		channels int
		app      gopus.Application
		name     string
	}{{1, gopus.ApplicationVoIP, "codec4-mono"}, {2, gopus.ApplicationAudio, "codec5-stereo"}} {
		if err := probe(probeCase.channels, probeCase.app); err != nil {
			panic(probeCase.name + ": " + err.Error())
		}
		fmt.Println(probeCase.name, "gopus<->"+C.GoString(C.opus_get_version_string())+" ok")
	}
}
