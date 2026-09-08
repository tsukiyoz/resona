package audio

import (
	"io/fs"
	"testing"
)

func TestBundledNotificationWAVsDecode(t *testing.T) {
	for kind, name := range notificationFiles {
		t.Run(kind, func(t *testing.T) {
			data, err := fs.ReadFile(notificationSounds, name)
			if err != nil {
				t.Fatal(err)
			}
			pcm, err := decodeNotificationWAV(data)
			if err != nil {
				t.Fatal(err)
			}
			if len(pcm) == 0 || len(pcm) > SampleRate*10 {
				t.Fatalf("samples = %d", len(pcm))
			}
		})
	}
}

func TestDecodeNotificationWAVRejectsMalformedInput(t *testing.T) {
	if _, err := decodeNotificationWAV([]byte("not a wav")); err == nil {
		t.Fatal("malformed WAV accepted")
	}
}
