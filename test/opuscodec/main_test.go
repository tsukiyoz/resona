package main

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestInputValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input.pcm")
	data := make([]byte, frameSize*4)
	binary.LittleEndian.PutUint32(data, math.Float32bits(float32(math.NaN())))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadPCM(path, 1); err == nil {
		t.Fatal("accepted NaN")
	}
	binary.LittleEndian.PutUint32(data, math.Float32bits(.25))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadPCM(path, 2); err == nil {
		t.Fatal("accepted truncated fixture")
	}
	pcm, _, err := loadPCM(path, 1)
	if err != nil || len(pcm) != frameSize || pcm[0] != .25 {
		t.Fatalf("PCM parsing: %v", err)
	}
}

func TestMetrics(t *testing.T) {
	if percentile([]int64{1000, 2000, 3000, 4000}, .99) != 4 {
		t.Fatal("wrong percentile")
	}
	if nrmse([]float32{1, -1}, []float32{1, -1}) != 0 {
		t.Fatal("identity must match")
	}
	if nrmse([]float32{0, 0}, []float32{1, -1}) != 1 {
		t.Fatal("silence must not pass parity")
	}
	if pcmHash(fixture(2)) != pcmHash(fixture(2)) {
		t.Fatal("fixture must be reproducible")
	}
}

func TestQualityAlignment(t *testing.T) {
	input := fixture(3)
	output := make([]float32, len(input))
	copy(output[312:], input)
	snr, delay := alignedSNR(input, output)
	if delay != 312 || snr < 100 {
		t.Fatalf("delay=%d SNR=%g", delay, snr)
	}
}

func TestReferenceRoundTrip(t *testing.T) {
	pcm := fixture(8)
	packets, err := packets(codecs[1], pcm, 32000, 5, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range packets {
		if len(p) != 80 {
			t.Fatalf("CBR packet size %d", len(p))
		}
	}
	output, err := decodeAll(codecs[1], packets)
	if err != nil || len(output) != len(pcm) {
		t.Fatalf("reference roundtrip: %v", err)
	}
}
