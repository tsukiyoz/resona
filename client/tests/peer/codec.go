package main

import (
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"

	"github.com/thesyncim/gopus"
)

func codecReference() error {
	var input struct {
		Packets [][]int `json:"packets"`
	}
	if err := json.NewDecoder(io.LimitReader(os.Stdin, 64<<10)).Decode(&input); err != nil {
		return err
	}
	if len(input.Packets) > 100 {
		return errors.New("too many test packets")
	}
	if len(input.Packets) == 0 {
		enc, err := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: 48000, Channels: 1, Application: gopus.ApplicationVoIP})
		if err != nil {
			return err
		}
		if err = enc.SetBitrate(48000); err != nil {
			return err
		}
		if err = enc.SetComplexity(5); err != nil {
			return err
		}
		pcm := make([]float32, 960)
		packet := make([]byte, 1024)
		for frame := range 10 {
			for i := range pcm {
				pcm[i] = float32(math.Sin(2*math.Pi*440*float64(i+frame*960)/48000) * 0.25)
			}
			n, err := enc.Encode(pcm, packet)
			if err != nil {
				return err
			}
			encoded := make([]int, n)
			for i := range encoded {
				encoded[i] = int(packet[i])
			}
			input.Packets = append(input.Packets, encoded)
		}
	}
	dec, err := gopus.NewDecoder(gopus.DecoderConfig{SampleRate: 48000, Channels: 1})
	if err != nil {
		return err
	}
	decoded := make([]float32, 0, len(input.Packets)*960)
	out := make([]float32, 5760)
	for _, raw := range input.Packets {
		if len(raw) == 0 || len(raw) > 1024 {
			return errors.New("invalid test packet")
		}
		data := make([]byte, len(raw))
		for i, v := range raw {
			if v < 0 || v > 255 {
				return errors.New("invalid byte")
			}
			data[i] = byte(v)
		}
		n, err := dec.Decode(data, out)
		if err != nil {
			return err
		}
		if n != 960 {
			return errors.New("unexpected frame length")
		}
		decoded = append(decoded, out[:n]...)
	}
	return json.NewEncoder(os.Stdout).Encode(struct {
		Packets [][]int   `json:"packets"`
		Decoded []float32 `json:"decoded"`
	}{input.Packets, decoded})
}
