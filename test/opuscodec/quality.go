package main

import (
	"fmt"
	"math"
	"strings"
)

type quality struct {
	Codec        string         `json:"codec"`
	Bitrate      int            `json:"bitrate"`
	ActualKbps   float64        `json:"actual_kbps"`
	AlignedSNRDB float64        `json:"aligned_snr_db"`
	DelaySamples int            `json:"delay_samples"`
	Modes        map[string]int `json:"packet_modes"`
}

// Search only codec-sized delays, without gain fitting. This is a waveform
// regression indicator, not a listening/perceptual score or codec ranking.
func alignedSNR(input, decoded []float32) (float64, int) {
	limit := min(rate, len(input)-frameSize)
	bestDelay := 0
	bestError := math.Inf(1)
	for delay := 0; delay <= frameSize; delay++ {
		var sum float64
		for i := 0; i < limit; i += 8 {
			delta := float64(input[i]) - float64(decoded[i+delay])
			sum += delta * delta
		}
		if sum < bestError {
			bestError = sum
			bestDelay = delay
		}
	}
	var noise, signal float64
	for i := 0; i < len(input)-bestDelay; i++ {
		sample := float64(input[i])
		delta := sample - float64(decoded[i+bestDelay])
		noise += delta * delta
		signal += sample * sample
	}
	return 10 * math.Log10(math.Max(signal, 1e-30)/math.Max(noise, 1e-30)), bestDelay
}
func inspectQuality(c codec, bitrate int, input, decoded []float32, packets [][]byte) quality {
	q := quality{Codec: c.name, Bitrate: bitrate, Modes: map[string]int{}}
	q.AlignedSNRDB, q.DelaySamples = alignedSNR(input, decoded)
	bytes := 0
	for _, p := range packets {
		bytes += len(p)
		mode := "silk"
		config := p[0] >> 3
		if config >= 16 {
			mode = "celt"
		} else if config >= 12 {
			mode = "hybrid"
		}
		q.Modes[mode]++
	}
	q.ActualKbps = float64(bytes) * 8 / (float64(len(packets)) * .02) / 1000
	return q
}
func qualityReport(values []quality) string {
	var out strings.Builder
	out.WriteString("\n## Encoder output checks\n\nDecoded with libopus; alignment searches 0..960 samples without gain fitting. SNR is only a waveform diagnostic, not perceptual quality. Includes warmup frames.\n\n| Codec | Target kbps | Actual kbps | Aligned SNR dB | Delay samples | SILK / hybrid / CELT frames |\n|---|---:|---:|---:|---:|---|\n")
	for _, q := range values {
		fmt.Fprintf(&out, "| %s | %d | %.2f | %.2f | %d | %d / %d / %d |\n", q.Codec, q.Bitrate/1000, q.ActualKbps, q.AlignedSNRDB, q.DelaySamples, q.Modes["silk"], q.Modes["hybrid"], q.Modes["celt"])
	}
	return out.String()
}
