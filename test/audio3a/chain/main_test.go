package main

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func testFixture() (manifest, fixture) {
	m := manifest{Version: 1, SampleRate: 48000, FrameSamples: 960, WarmupMS: 100, DurationMS: 1000, Repeats: 1}
	f := fixture{scene: scene{Name: "test", SpeechMS: [][2]int{{100, 700}}}, clean: make([]float32, 52800), capture: make([]float32, 52800)}
	for i := range f.capture {
		if i >= 9600 && i < 38400 {
			f.clean[i] = float32(math.Sin(float64(i)*.04) * .08)
		}
		f.capture[i] = f.clean[i] + float32(math.Sin(float64(i)*.9)*.002)
	}
	return m, f
}

func TestIndependentRoundtripsAndPacketShape(t *testing.T) {
	m, f := testFixture()
	for _, ns := range []string{"off", "speex", "rnnoise"} {
		r, a, err := run(f, m, ns, 32000, 1)
		if err != nil {
			t.Fatal(err)
		}
		_, b, err := run(f, m, ns, 32000, 2)
		if err != nil {
			t.Fatal(err)
		}
		if r.Packets != 50 || r.AGCActive > 50 || r.PayloadKbps <= 0 {
			t.Fatalf("invalid diagnostics: %+v", r)
		}
		if ns == "off" && r.AGCActive == 0 {
			t.Fatal("active bypass input never reached AGC")
		}
		for i := range a {
			if len(a[i]) != 48000 || !slices.Equal(a[i], b[i]) {
				t.Fatalf("%s stage %d has wrong length or leaked state", ns, i)
			}
		}
		if slices.Equal(a[0], a[1]) {
			t.Fatal("codec was bypassed")
		}
		if ns == "off" && !slices.Equal(a[0], f.capture[4800:]) {
			t.Fatal("bypass changed PCM")
		}
	}
	p, err := newProcessor(0)
	if err != nil {
		t.Fatal(err)
	}
	p.close()
	p.close()
	if p.process(make([]float32, 960)) == nil {
		t.Fatal("used closed processor")
	}
}

func TestManifestHashesAndBounds(t *testing.T) {
	m, f := testFixture()
	dir := t.TempDir()
	pcm := make([]byte, len(f.capture)*4)
	for i, x := range f.capture {
		binary.LittleEndian.PutUint32(pcm[i*4:], math.Float32bits(x))
	}
	if err := os.WriteFile(filepath.Join(dir, "capture.f32"), pcm, 0o600); err != nil {
		t.Fatal(err)
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(pcm))
	f.Clean, f.Capture, f.CleanHash, f.CaptureHash = "capture.f32", "capture.f32", hash, hash
	m.Scenes = []scene{f.scene}
	path := filepath.Join(dir, "manifest.json")
	if err := saveJSON(path, m); err != nil {
		t.Fatal(err)
	}
	if _, _, err := load(path); err != nil {
		t.Fatal(err)
	}
	if _, err := readPCM(dir, "../capture.f32", hash, len(f.capture)); err == nil {
		t.Fatal("accepted escaping path")
	}
	if _, err := readPCM(dir, "capture.f32", "wrong", len(f.capture)); err == nil {
		t.Fatal("accepted bad hash")
	}
	if _, err := readPCM(dir, "capture.f32", hash, len(f.capture)-1); err == nil {
		t.Fatal("accepted wrong size")
	}
	m.Scenes[0].SpeechMS = [][2]int{{-1, 500}}
	_ = saveJSON(path, m)
	if _, _, err := load(path); err == nil {
		t.Fatal("accepted negative speech region")
	}
}

func TestPauseMetricsAndActivityGate(t *testing.T) {
	pcm := make([]float32, 48000)
	for i := range pcm {
		pcm[i] = .001
	}
	for i := 4800; i < 28800; i++ {
		pcm[i] = .1
	}
	r := inspect(pcm, [][2]int{{100, 600}})
	if r.PauseDBFS == nil || math.Abs(*r.PauseDBFS+60) > .01 || math.Abs(r.SpeechDBFS+20) > .01 {
		t.Fatalf("wrong levels: %+v", r)
	}
	if hasActivity(make([]float32, 960)) {
		t.Fatal("silence trained AGC")
	}
	if !hasActivity(pcm[:960]) {
		t.Fatal("threshold activity was lost")
	}
}
