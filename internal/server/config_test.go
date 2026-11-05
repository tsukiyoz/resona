package server

import (
	"fmt"
	"strings"
	"testing"
)

func TestVoiceQueueConfiguration(t *testing.T) {
	for _, slots := range []int{0, 1, 32, MaxVoiceQueueSlots} {
		t.Run(fmt.Sprint(slots), func(t *testing.T) {
			s, err := Listen("127.0.0.1:0", Config{
				Store: testStore(t, t.TempDir()), Name: "Queue test",
				NoiseKey: []byte(strings.Repeat("k", 32)), VoiceQueueSlots: slots,
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s.listener.Close() })
			want := slots
			if want == 0 {
				want = 16
			}
			if s.config.VoiceQueueSlots != want {
				t.Fatalf("effective slots=%d want=%d", s.config.VoiceQueueSlots, want)
			}
		})
	}
	for _, slots := range []int{-1, MaxVoiceQueueSlots + 1} {
		if _, err := Listen("invalid-address", Config{VoiceQueueSlots: slots}); err == nil || !strings.Contains(err.Error(), "voice queue slots") {
			t.Fatalf("invalid capacity %d was not rejected before startup: %v", slots, err)
		}
	}
}
