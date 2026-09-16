package audio

import (
	"context"
	"testing"
)

func TestOutputGainUpdatesLiveWithoutDeviceRestart(t *testing.T) {
	devices := &fakeDeviceFactory{}
	e := newWithFactory(monitorTransport{}, nil, devices)
	e.monitor = true
	defer e.Close()
	if err := e.Configure(context.Background(), VoiceConfig{Enabled: true, Muted: true, Volume: 100}); err != nil {
		t.Fatal(err)
	}
	for _, volume := range []int{200, 794, 10, 0} {
		if err := e.SetOutputGain(volume); err != nil {
			t.Fatal(err)
		}
		if e.Status().Config.Volume != volume || e.run.currentConfig().Volume != volume {
			t.Fatal("gain not published")
		}
	}
	if len(devices.opens) != 1 || !e.Status().Config.Muted {
		t.Fatal("gain changed devices or mute")
	}
	e.ops <- struct{}{}
	if err := e.SetOutputGain(100); err == nil {
		t.Fatal("gain bypassed lifecycle exclusion")
	}
	e.release()
}
