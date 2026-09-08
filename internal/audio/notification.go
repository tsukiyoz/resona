package audio

import (
	"context"
	"embed"
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"sync/atomic"
	"time"
)

//go:embed sounds/*.wav
var notificationSounds embed.FS

var notificationFiles = map[string]string{
	"connected":     "sounds/connected.wav",
	"disconnected":  "sounds/disconnected.wav",
	"member_joined": "sounds/member-joined.wav",
	"member_left":   "sounds/member-left.wav",
}

// PlayNotification plays one bundled local notification through the default
// shared output device. It never opens an input device or sends network audio.
func PlayNotification(ctx context.Context, kind string, volume int) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if volume < 0 || volume > 100 {
		return errors.New("提示音音量必须在 0 到 100 之间")
	}
	name, ok := notificationFiles[kind]
	if !ok {
		return errors.New("未知的提示音类型")
	}
	wav, err := fs.ReadFile(notificationSounds, name)
	if err != nil {
		return fmt.Errorf("无法读取提示音: %w", err)
	}
	mono, err := decodeNotificationWAV(wav)
	if err != nil {
		return err
	}
	stereo := make([]float32, len(mono)*2)
	gain := float32(volume) / 100
	for i, sample := range mono {
		stereo[i*2], stereo[i*2+1] = sample*gain, sample*gain
	}
	var position atomic.Uint64
	session, err := defaultDevices.Open(VoiceConfig{Volume: volume}, true, false, deviceCallbacks{playback: func(output []float32) {
		start := int(position.Load())
		if start >= len(stereo) {
			return
		}
		n := copy(output, stereo[start:])
		position.Add(uint64(n))
	}})
	if err != nil {
		return err
	}
	duration := time.Duration(float64(len(mono))/SampleRate*float64(time.Second)) + 100*time.Millisecond
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		_ = session.Close()
		return ctx.Err()
	case <-timer.C:
		return session.Close()
	}
}

func decodeNotificationWAV(data []byte) ([]float32, error) {
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, errors.New("提示音不是有效的 WAV 文件")
	}
	var format []byte
	var pcm []byte
	for offset := 12; offset+8 <= len(data); {
		size := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		start, end := offset+8, offset+8+size
		if size < 0 || end < start || end > len(data) {
			return nil, errors.New("提示音 WAV 数据已损坏")
		}
		switch string(data[offset : offset+4]) {
		case "fmt ":
			format = data[start:end]
		case "data":
			pcm = data[start:end]
		}
		offset = end + size%2
	}
	if len(format) < 16 || binary.LittleEndian.Uint16(format[0:2]) != 1 || binary.LittleEndian.Uint16(format[2:4]) != 1 || binary.LittleEndian.Uint32(format[4:8]) != SampleRate || binary.LittleEndian.Uint16(format[14:16]) != 16 {
		return nil, errors.New("提示音必须是 48 kHz、16 位、单声道 PCM WAV")
	}
	if len(pcm) == 0 || len(pcm)%2 != 0 || len(pcm) > SampleRate*2*10 {
		return nil, errors.New("提示音 PCM 数据长度无效")
	}
	out := make([]float32, len(pcm)/2)
	for i := range out {
		sample := int16(binary.LittleEndian.Uint16(pcm[i*2 : i*2+2]))
		out[i] = float32(sample) / float32(math.MaxInt16+1)
	}
	return out, nil
}
