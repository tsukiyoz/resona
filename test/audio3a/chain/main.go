package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/thesyncim/gopus"
)

const frameSize = 960

type scene struct {
	Name        string   `json:"name"`
	Clean       string   `json:"clean"`
	Capture     string   `json:"capture"`
	CleanHash   string   `json:"clean_sha256"`
	CaptureHash string   `json:"capture_sha256"`
	SpeechMS    [][2]int `json:"speech_ms"`
}

type manifest struct {
	Version        int             `json:"version"`
	SampleRate     int             `json:"sample_rate"`
	FrameSamples   int             `json:"frame_samples"`
	WarmupMS       int             `json:"warmup_ms"`
	DurationMS     int             `json:"duration_ms"`
	Repeats        int             `json:"repeats"`
	Sources        json.RawMessage `json:"sources"`
	SourceManifest json.RawMessage `json:"source_manifest"`
	Scenes         []scene         `json:"scenes"`
}

type fixture struct {
	scene
	clean, capture []float32
}

type levels struct {
	SpeechDBFS     float64   `json:"speech_dbfs"`
	PauseDBFS      *float64  `json:"pause_dbfs"`
	UtterancesDBFS []float64 `json:"utterances_dbfs"`
	Peak           float64   `json:"peak"`
	FullScale      int       `json:"full_scale_samples"`
}

type result struct {
	Scene       string  `json:"scene"`
	NS          string  `json:"ans"`
	Model       string  `json:"rnnoise_model"`
	Bitrate     int     `json:"bitrate"`
	Repeat      int     `json:"repeat"`
	Packets     int     `json:"packets"`
	MeanUS      float64 `json:"mean_us"`
	P99US       float64 `json:"p99_us"`
	MaxUS       float64 `json:"max_us"`
	CPU         float64 `json:"cpu_percent_one_core"`
	GoAllocs    float64 `json:"go_allocs_per_packet"`
	PayloadKbps float64 `json:"payload_kbps"`
	AGCActive   int     `json:"agc_active_packets"`
	ANS         levels  `json:"ans_levels"`
	Decoded     levels  `json:"decoded_levels"`
	Received    levels  `json:"received_levels"`
}

func main() {
	input := flag.String("input", "chain-input/manifest.json", "prepared, hash-verified float PCM manifest")
	out := flag.String("out", "chain-results", "new result directory")
	rates := flag.String("bitrates", "32000,48000", "comma-separated rates, 16000..64000")
	repeats := flag.Int("repeats", 0, "override manifest repeats (1..10)")
	flag.Parse()
	if err := execute(*input, *out, *rates, *repeats); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func safeName(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func readPCM(dir, name, hash string, samples int) ([]float32, error) {
	if name == "" || filepath.Base(name) != name || strings.ContainsAny(name, `/\:`) {
		return nil, errors.New("PCM path must be a filename")
	}
	f, err := os.Open(filepath.Join(dir, name))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, int64(samples*4+1)))
	if err != nil || len(data) != samples*4 {
		return nil, fmt.Errorf("%s: expected exactly %d float32 samples", name, samples)
	}
	if fmt.Sprintf("%x", sha256.Sum256(data)) != hash {
		return nil, fmt.Errorf("%s: PCM checksum mismatch", name)
	}
	pcm := make([]float32, samples)
	for i := range pcm {
		pcm[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[i*4:]))
		if !finite(pcm[i]) || math.Abs(float64(pcm[i])) > 1 {
			return nil, fmt.Errorf("%s: invalid normalized PCM", name)
		}
	}
	return pcm, nil
}

func finite(x float32) bool { return !math.IsNaN(float64(x)) && !math.IsInf(float64(x), 0) }

func load(path string) (manifest, []fixture, error) {
	var m manifest
	f, err := os.Open(path)
	if err != nil {
		return m, nil, err
	}
	defer f.Close()
	dec := json.NewDecoder(io.LimitReader(f, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return m, nil, err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return m, nil, errors.New("trailing manifest data")
	}
	if m.Version != 1 || m.SampleRate != 48000 || m.FrameSamples != frameSize || m.WarmupMS < 100 || m.WarmupMS > 10000 || m.WarmupMS%20 != 0 || m.DurationMS < 1000 || m.DurationMS > 60000 || m.DurationMS%20 != 0 || m.Repeats < 1 || m.Repeats > 10 || len(m.Scenes) < 1 || len(m.Scenes) > 20 {
		return m, nil, errors.New("invalid chain manifest bounds")
	}
	samples := (m.WarmupMS + m.DurationMS) * 48
	if samples*8*len(m.Scenes) > 128<<20 {
		return m, nil, errors.New("input exceeds 128MiB PCM budget")
	}
	seen := make(map[string]bool)
	var fixtures []fixture
	for _, s := range m.Scenes {
		if !safeName(s.Name) || seen[s.Name] || len(s.SpeechMS) == 0 || len(s.SpeechMS) > 100 {
			return m, nil, errors.New("invalid/duplicate scene or speech regions")
		}
		seen[s.Name] = true
		end := 0
		for _, region := range s.SpeechMS {
			if region[0] < end || region[1]-region[0] <= 200 || region[1] > m.DurationMS {
				return m, nil, errors.New("invalid speech region")
			}
			end = region[1]
		}
		clean, err := readPCM(filepath.Dir(path), s.Clean, s.CleanHash, samples)
		if err != nil {
			return m, nil, err
		}
		capture, err := readPCM(filepath.Dir(path), s.Capture, s.CaptureHash, samples)
		if err != nil {
			return m, nil, err
		}
		fixtures = append(fixtures, fixture{s, clean, capture})
	}
	return m, fixtures, nil
}

func newEncoder(bitrate int) (*gopus.Encoder, error) {
	e, err := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: 48000, Channels: 1, Application: gopus.ApplicationVoIP})
	if err != nil {
		return nil, err
	}
	if err := e.SetBitrate(bitrate); err != nil {
		return nil, err
	}
	if err := e.SetComplexity(5); err != nil {
		return nil, err
	}
	return e, nil
}

func hasActivity(pcm []float32) bool {
	var energy float64
	for _, x := range pcm {
		energy += float64(x * x)
	}
	return energy/float64(len(pcm)) >= 0.000001
}

func run(f fixture, m manifest, ns string, bitrate, repeat int) (result, [3][]float32, error) {
	r := result{Scene: f.Name, NS: ns, Model: modelName(), Bitrate: bitrate, Repeat: repeat, Packets: m.DurationMS / 20}
	var output [3][]float32
	kind, ok := map[string]int{"off": 0, "rnnoise": 1, "speex": 2}[ns]
	if !ok {
		return r, output, errors.New("invalid ANS")
	}
	send, err := newProcessor(kind)
	if err != nil {
		return r, output, err
	}
	defer send.close()
	receive, err := newProcessor(3)
	if err != nil {
		return r, output, err
	}
	defer receive.close()
	encoder, err := newEncoder(bitrate)
	if err != nil {
		return r, output, err
	}
	decoder, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 1))
	if err != nil {
		return r, output, err
	}
	warm := m.WarmupMS / 20
	for i := range output {
		output[i] = make([]float32, m.DurationMS*48)
	}
	times := make([]float64, r.Packets)
	frame, decoded := make([]float32, frameSize), make([]float32, frameSize)
	packet := make([]byte, 1275)
	var start time.Time
	var cpuStart int64
	var before, after runtime.MemStats
	bytesSent := 0
	for tick := 0; tick < len(f.capture)/frameSize; tick++ {
		if tick == warm {
			runtime.ReadMemStats(&before)
			cpuStart = cpuTime()
			start = time.Now()
		}
		at := time.Now()
		copy(frame, f.capture[tick*frameSize:(tick+1)*frameSize])
		if err := send.process(frame); err != nil {
			return r, output, err
		}
		if tick >= warm {
			copy(output[0][(tick-warm)*frameSize:], frame)
		}
		n, err := encoder.Encode(frame, packet)
		if err != nil || n <= 0 || n > len(packet) {
			return r, output, fmt.Errorf("encode: length=%d error=%v", n, err)
		}
		if _, err := gopus.ParsePacket(packet[:n]); err != nil {
			return r, output, err
		}
		count, err := decoder.Decode(packet[:n], decoded)
		if err != nil || count != frameSize {
			return r, output, fmt.Errorf("decode: samples=%d error=%v", count, err)
		}
		if tick >= warm {
			copy(output[1][(tick-warm)*frameSize:], decoded)
		}
		if hasActivity(decoded) {
			if err := receive.process(decoded); err != nil {
				return r, output, err
			}
			if tick >= warm {
				r.AGCActive++
			}
		}
		if tick >= warm {
			copy(output[2][(tick-warm)*frameSize:], decoded)
			bytesSent += n
			times[tick-warm] = float64(time.Since(at).Nanoseconds()) / 1000
		}
	}
	elapsed, cpu := time.Since(start), cpuTime()-cpuStart
	runtime.ReadMemStats(&after)
	r.MeanUS = float64(elapsed.Nanoseconds()) / 1000 / float64(r.Packets)
	r.CPU = float64(cpu) / float64(m.DurationMS*1_000_000) * 100
	r.GoAllocs = float64(after.Mallocs-before.Mallocs) / float64(r.Packets)
	r.PayloadKbps = float64(bytesSent*8) / float64(m.DurationMS)
	sort.Float64s(times)
	r.P99US = times[max(0, int(math.Ceil(float64(len(times))*.99))-1)]
	r.MaxUS = times[len(times)-1]
	for _, stage := range output {
		for _, x := range stage {
			if !finite(x) || math.Abs(float64(x)) > 8 {
				return r, output, errors.New("nonfinite/excessive output")
			}
		}
	}
	r.ANS, r.Decoded, r.Received = inspect(output[0], f.SpeechMS), inspect(output[1], f.SpeechMS), inspect(output[2], f.SpeechMS)
	return r, output, nil
}

func db(energy float64) float64 { return 10 * math.Log10(max(1e-12, energy)) }

func inspect(pcm []float32, speech [][2]int) levels {
	r := levels{}
	var voiced, noise float64
	var nv, nn int
	for _, x := range pcm {
		r.Peak = max(r.Peak, math.Abs(float64(x)))
		if math.Abs(float64(x)) >= 1 {
			r.FullScale++
		}
	}
	for _, region := range speech {
		a, b := (region[0]+100)*48, (region[1]-100)*48
		var energy float64
		for _, x := range pcm[a:b] {
			energy += float64(x) * float64(x)
		}
		voiced += energy
		nv += b - a
		r.UtterancesDBFS = append(r.UtterancesDBFS, db(energy/float64(max(1, b-a))))
	}
	end := 0
	for _, region := range append(append([][2]int(nil), speech...), [2]int{len(pcm) / 48, len(pcm) / 48}) {
		a, b := (end+100)*48, (region[0]-100)*48
		if b > a {
			for _, x := range pcm[a:b] {
				noise += float64(x) * float64(x)
			}
			nn += b - a
		}
		end = region[1]
	}
	r.SpeechDBFS = db(voiced / float64(max(1, nv)))
	if nn > 0 {
		d := db(noise / float64(nn))
		r.PauseDBFS = &d
	}
	return r
}

func writeWAV(path string, pcm []float32) error {
	var b bytes.Buffer
	b.WriteString("RIFF")
	_ = binary.Write(&b, binary.LittleEndian, uint32(36+len(pcm)*4))
	b.WriteString("WAVEfmt ")
	for _, v := range []any{uint32(16), uint16(3), uint16(1), uint32(48000), uint32(48000 * 4), uint16(4), uint16(32)} {
		_ = binary.Write(&b, binary.LittleEndian, v)
	}
	b.WriteString("data")
	_ = binary.Write(&b, binary.LittleEndian, uint32(len(pcm)*4))
	_ = binary.Write(&b, binary.LittleEndian, pcm)
	return os.WriteFile(path, b.Bytes(), 0o600)
}

func saveJSON(path string, value any) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o600)
}

func execute(input, out, rates string, repeats int) error {
	m, fixtures, err := load(input)
	if err != nil {
		return err
	}
	if repeats != 0 {
		if repeats < 1 || repeats > 10 {
			return errors.New("repeats must be 1..10")
		}
		m.Repeats = repeats
	}
	var bitrates []int
	seen := make(map[int]bool)
	for _, s := range strings.Split(rates, ",") {
		n, err := strconv.Atoi(s)
		if err != nil || n < 16000 || n > 64000 || seen[n] || len(bitrates) >= 4 {
			return errors.New("provide 1..4 unique bitrates, 16000..64000")
		}
		seen[n] = true
		bitrates = append(bitrates, n)
	}
	if err := os.Mkdir(out, 0o700); err != nil {
		return fmt.Errorf("output must be a new directory: %w", err)
	}
	if err := os.Mkdir(filepath.Join(out, "raw"), 0o700); err != nil {
		return err
	}
	encoder, err := newEncoder(bitrates[0])
	if err != nil {
		return err
	}
	metadata := map[string]any{"input": m, "rnnoise_model": modelName(), "go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH, "gopus_module": "github.com/thesyncim/gopus v0.1.1", "gopus_reported_version": gopus.VersionString(), "opus": "48k mono,20ms,VoIP,complexity5; bitrate/FEC/DTX defaults match core", "bitrate_mode": encoder.BitrateMode(), "vbr": encoder.VBR(), "vbr_constraint": encoder.VBRConstraint(), "bitrates": bitrates, "agc2": "independent per-peer; headroom5dB,max18dB,initial0dB; skip RMS energy <1e-6 like core", "limits": "No devices, networking, jitter, PLC, mixing, global gain or output limiter. Same algorithms/configs, benchmark FFI adapter. Stage copies/timing overhead included; construction, analysis and disk I/O excluded. CPU is throughput of one real-time core, not gaming frame impact. Go allocation count excludes native allocations."}
	if err := saveJSON(filepath.Join(out, "metadata.json"), metadata); err != nil {
		return err
	}
	sources, err := os.ReadFile(filepath.Join(filepath.Dir(input), "SOURCES.md"))
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(out, "SOURCES.md"), sources, 0o600); err != nil {
		return err
	}
	var rows []result
	for _, f := range fixtures {
		if err := writeWAV(filepath.Join(out, "raw", f.Name+"-clean.wav"), f.clean[m.WarmupMS*48:]); err != nil {
			return err
		}
		if err := writeWAV(filepath.Join(out, "raw", f.Name+"-input.wav"), f.capture[m.WarmupMS*48:]); err != nil {
			return err
		}
		for _, bitrate := range bitrates {
			for repeat := 1; repeat <= m.Repeats; repeat++ {
				choices := []string{"off", "speex", "rnnoise"}
				for j := range choices {
					ns := choices[(j+repeat-1)%len(choices)]
					fmt.Fprintf(os.Stderr, "chain %s %s %s %d repeat %d\n", f.Name, ns, modelName(), bitrate, repeat)
					r, audio, err := run(f, m, ns, bitrate, repeat)
					if err != nil {
						return err
					}
					rows = append(rows, r)
					if repeat == 1 {
						for i, stage := range []string{"ans", "decoded", "agc2"} {
							name := fmt.Sprintf("%s-%s-%d-%s.wav", f.Name, ns, bitrate, stage)
							if err := writeWAV(filepath.Join(out, "raw", name), audio[i]); err != nil {
								return err
							}
						}
					}
				}
			}
		}
	}
	if err := saveJSON(filepath.Join(out, "results.json"), rows); err != nil {
		return err
	}
	var csvBytes bytes.Buffer
	w := csv.NewWriter(&csvBytes)
	_ = w.Write([]string{"scene", "ans", "model", "bitrate", "repeat", "mean_us", "p99_us", "cpu_percent_one_core", "go_allocs_per_packet", "payload_kbps", "agc_active_packets", "decoded_pause_dbfs", "received_pause_dbfs", "received_full_scale_samples"})
	value := func(x *float64) string {
		if x == nil {
			return ""
		}
		return fmt.Sprintf("%.3f", *x)
	}
	for _, r := range rows {
		_ = w.Write([]string{r.Scene, r.NS, r.Model, strconv.Itoa(r.Bitrate), strconv.Itoa(r.Repeat), fmt.Sprintf("%.3f", r.MeanUS), fmt.Sprintf("%.3f", r.P99US), fmt.Sprintf("%.4f", r.CPU), fmt.Sprintf("%.3f", r.GoAllocs), fmt.Sprintf("%.3f", r.PayloadKbps), strconv.Itoa(r.AGCActive), value(r.Decoded.PauseDBFS), value(r.Received.PauseDBFS), strconv.Itoa(r.Received.FullScale)})
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(out, "results.csv"), csvBytes.Bytes(), 0o600); err != nil {
		return err
	}
	var report strings.Builder
	report.WriteString("# ANS -> gopus -> receiver AGC2\n\nOne sender and one independent receiver per run, 48k mono/20ms, VoIP complexity 5. Raw WAVs preserve gain: compare `ans`, `decoded`, then `agc2` files with the same scene/rate/backend. CPU is normalized throughput of one real-time core, not game-frame impact. Construction, analysis and disk I/O are excluded; validation and stage-copy overhead are included. Go allocation counts exclude native allocations. No AEC/devices/network/jitter/PLC/mixing/manual gain/final limiter. See metadata.json for versions and configuration.\n\nPause RMS excludes 100ms around speech boundaries; no waveform alignment. Region RMS includes residual noise, not isolated speech loudness. Values are medians across repeats.\n\n| Scene | ANS | Rate | CPU % | p99 us | Go allocs/packet | Payload kbps | Pause decoded/AGC2 dBFS |\n|---|---|---:|---:|---:|---:|---:|---|\n")
	for _, f := range fixtures {
		for _, bitrate := range bitrates {
			for _, ns := range []string{"off", "speex", "rnnoise"} {
				var group []result
				for _, r := range rows {
					if r.Scene == f.Name && r.Bitrate == bitrate && r.NS == ns {
						group = append(group, r)
					}
				}
				median := func(get func(result) float64) float64 {
					values := make([]float64, len(group))
					for i, r := range group {
						values[i] = get(r)
					}
					sort.Float64s(values)
					return values[len(values)/2]
				}
				fmt.Fprintf(&report, "| %s | %s | %d | %.3f | %.1f | %.2f | %.2f | %s/%s |\n", f.Name, ns, bitrate, median(func(r result) float64 { return r.CPU }), median(func(r result) float64 { return r.P99US }), median(func(r result) float64 { return r.GoAllocs }), median(func(r result) float64 { return r.PayloadKbps }), value(group[0].Decoded.PauseDBFS), value(group[0].Received.PauseDBFS))
			}
		}
	}
	if err := os.WriteFile(filepath.Join(out, "report.md"), []byte(report.String()), 0o600); err != nil {
		return err
	}
	fmt.Println(out)
	return nil
}
