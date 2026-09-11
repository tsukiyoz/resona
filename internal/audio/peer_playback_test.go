package audio

import (
	"math"
	"sync"
	"testing"
	"time"
)

func TestPeerGainMixesOnlyTheSelectedSpeaker(t *testing.T) {
	for _, codec := range []Codec{CodecOpusVoice, CodecOpusMusic} {
		for _, tc := range []struct {
			volume int
			muted  bool
			want   float32
		}{
			{0, false, .1}, {100, false, .35}, {200, false, .6}, {150, true, .1}, {150, false, .475},
		} {
			e := &Engine{}
			e.SetPeerPlayback(map[uint16]PeerPlayback{1: {Instance: "a", Volume: tc.volume, Muted: tc.muted}, 2: {Instance: "b", Volume: 100}})
			mix := make([]float32, FrameSamples*2)
			for id, instance := range map[uint16]string{1: "a", 2: "b"} {
				speaker, err := newSpeaker(codec)
				if err != nil {
					t.Fatal(err)
				}
				value := float32(.1)
				if id == 1 {
					value = .25
				}
				speaker.pcm = make([]float32, FrameSamples*speaker.channels)
				for i := range speaker.pcm {
					speaker.pcm[i] = value
				}
				gain, current := peerGain(e.peers.Load(), id, instance)
				if !current {
					t.Fatal("current peer rejected")
				}
				if _, _, _, _, err := speaker.render(mix, gain, time.Now()); err != nil {
					t.Fatal(err)
				}
			}
			for _, sample := range mix {
				if math.Abs(float64(sample-tc.want)) > .00001 {
					t.Fatalf("codec=%d volume=%d muted=%t got=%f want=%f", codec, tc.volume, tc.muted, sample, tc.want)
				}
			}
		}
	}
}

func TestPeerSnapshotIsOwnedAndRejectsDepartedInstances(t *testing.T) {
	e := &Engine{}
	peers := map[uint16]PeerPlayback{1: {Instance: "old", Volume: 150, Muted: true}}
	e.SetPeerPlayback(peers)
	peers[1] = PeerPlayback{Instance: "new", Volume: 100}
	if gain, current := peerGain(e.peers.Load(), 1, "old"); gain != 0 || !current {
		t.Fatal("caller changed immutable mixer state")
	}
	e.SetPeerPlayback(peers)
	if _, current := peerGain(e.peers.Load(), 1, "old"); current {
		t.Fatal("old queued voice can enter new member's mix")
	}
	if gain, current := peerGain(e.peers.Load(), 1, "new"); gain != 1 || !current {
		t.Fatal("new member inherited old mute")
	}
	e.SetPeerPlayback(nil)
	if _, current := peerGain(e.peers.Load(), 1, "new"); current {
		t.Fatal("departed member still audible")
	}
}

func TestPeerUpdatesAndMixerReadsCanRunConcurrently(t *testing.T) {
	e := &Engine{}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			e.SetPeerPlayback(map[uint16]PeerPlayback{1: {Instance: "one", Volume: i % 201}})
		}
	}()
	for i := 0; i < 1000; i++ {
		peerGain(e.peers.Load(), 1, "one")
	}
	wg.Wait()
}
