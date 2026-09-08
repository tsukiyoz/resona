package audio

import (
	"context"
	"io/fs"
	"testing"
	"time"
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

func TestNotificationWaitsForDeviceConsumption(t *testing.T) {
	devices := &fakeDeviceFactory{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- playNotification(ctx, "disconnected", 50, devices) }()
	deadline := time.Now().Add(time.Second)
	for {
		devices.mu.Lock()
		opened := len(devices.opens) > 0
		devices.mu.Unlock()
		if opened {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("device did not open")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case err := <-done:
		t.Fatalf("completed before callback: %v", err)
	default:
	}
	call := devices.last()
	if call.capture || !call.playback {
		t.Fatal("notification opened input")
	}
	output := make([]float32, SampleRate*20)
	call.callbacks.playback(output)
	select {
	case err := <-done:
		t.Fatalf("completed before final buffer consumed: %v", err)
	default:
	}
	call.callbacks.playback(output)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("consumed notification did not finish")
	}
	if !call.session.closed {
		t.Fatal("notification did not close output")
	}
}

func TestNotificationCancellationIsNotPlaybackSuccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := playNotification(ctx, "disconnected", 50, &fakeDeviceFactory{})
	if err == nil {
		t.Fatal("cancelled notification reported completion")
	}
}

func TestDecodeNotificationWAVRejectsMalformedInput(t *testing.T) {
	if _, err := decodeNotificationWAV([]byte("not a wav")); err == nil {
		t.Fatal("malformed WAV accepted")
	}
}
