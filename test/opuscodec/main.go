package main

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
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

const rate = 48000
const frameSize = 960
const warmFrames = 50
const maxPacket = 1275

type encoder interface {
	Encode([]float32, []byte) (int, error)
	Close()
}
type decoder interface {
	Decode([]byte, []float32) (int, error)
	Close()
}
type goEncoder struct{ *gopus.Encoder }

func (*goEncoder) Close() {}

type goDecoder struct{ *gopus.Decoder }

func (*goDecoder) Close() {}

type codec struct {
	name string
	kind int
}

var codecs = []codec{{"gopus", -1}, {"libopus", 0}, {"opus-rs", 1}, {"rusty-opus", 2}}

func (c codec) encoder(bitrate, complexity int, cbr bool) (encoder, error) {
	if c.kind >= 0 {
		return newNativeEncoder(c.kind, bitrate, complexity, cbr)
	}
	e, err := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: rate, Channels: 1, Application: gopus.ApplicationVoIP})
	if err != nil {
		return nil, err
	}
	if err = e.SetBitrate(bitrate); err != nil {
		return nil, err
	}
	if err = e.SetComplexity(complexity); err != nil {
		return nil, err
	}
	e.SetVBR(!cbr)
	e.SetVBRConstraint(true)
	e.SetDTX(false)
	e.SetFEC(false)
	if err = e.SetPacketLoss(0); err != nil {
		return nil, err
	}
	return &goEncoder{e}, nil
}
func (c codec) decoder() (decoder, error) {
	if c.kind >= 0 {
		return newNativeDecoder(c.kind)
	}
	d, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(rate, 1))
	if err != nil {
		return nil, err
	}
	return &goDecoder{d}, nil
}

type result struct {
	Codec      string  `json:"codec"`
	Operation  string  `json:"operation"`
	Bitrate    int     `json:"bitrate"`
	Streams    int     `json:"streams"`
	Repeat     int     `json:"repeat"`
	Ticks      int     `json:"ticks"`
	WallUS     float64 `json:"wall_us_per_tick"`
	CPUUS      float64 `json:"cpu_us_per_tick"`
	P50US      float64 `json:"p50_us"`
	P99US      float64 `json:"p99_us"`
	P999US     float64 `json:"p999_us"`
	MaxUS      float64 `json:"max_us"`
	GoAllocs   float64 `json:"go_allocs_per_tick"`
	ActualKbps float64 `json:"actual_kbps"`
	Checksum   float64 `json:"checksum"`
	Error      string  `json:"error,omitempty"`
}
type interop struct {
	Encoder string  `json:"encoder"`
	Decoder string  `json:"decoder"`
	Bitrate int     `json:"bitrate"`
	NRMSE   float64 `json:"nrmse_vs_libopus_decoder"`
	Error   string  `json:"error,omitempty"`
}

func fixture(frames int) []float32 {
	pcm := make([]float32, frames*frameSize)
	var phase float64
	seed := uint32(0x5245534f)
	for i := range pcm {
		t := float64(i) / rate
		seed = seed*1664525 + 1013904223
		noise := float64(seed)/math.MaxUint32*2 - 1
		phase += 2 * math.Pi * (130 + 45*math.Sin(2*math.Pi*.7*t)) / rate
		envelope := .15 + .1*math.Sin(2*math.Pi*3*t)
		x := envelope * (math.Sin(phase) + .35*math.Sin(2*phase) + .15*math.Sin(5*phase))
		// Alternate voiced harmonics, broadband noise, and quiet intervals.
		switch int(t*2) % 6 {
		case 3:
			x = .12 * noise
		case 4:
			x *= .02
		default:
			x += .005 * noise
		}
		pcm[i] = float32(x)
	}
	return pcm
}
func loadPCM(path string, frames int) ([]float32, string, error) {
	if path == "" {
		return fixture(frames), "synthetic-harmonics-noise-quiet-v1 (not speech quality evidence)", nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	if len(data)%4 != 0 || len(data)/4 < frames*frameSize {
		return nil, "", fmt.Errorf("PCM must contain at least %d float32 samples at 48kHz mono", frames*frameSize)
	}
	pcm := make([]float32, frames*frameSize)
	for i := range pcm {
		pcm[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[4*i:]))
		if !finite(float64(pcm[i])) || math.Abs(float64(pcm[i])) > 1 {
			return nil, "", fmt.Errorf("invalid normalized PCM sample %d", i)
		}
	}
	return pcm, "external-f32le-48k-mono", nil
}
func pcmHash(pcm []float32) string {
	data := make([]byte, len(pcm)*4)
	for i, x := range pcm {
		binary.LittleEndian.PutUint32(data[i*4:], math.Float32bits(x))
	}
	return fmt.Sprintf("%x", sha256.Sum256(data))
}
func finite(x float64) bool { return !math.IsNaN(x) && !math.IsInf(x, 0) }

func packets(c codec, pcm []float32, bitrate, complexity int, cbr bool) ([][]byte, error) {
	e, err := c.encoder(bitrate, complexity, cbr)
	if err != nil {
		return nil, err
	}
	defer e.Close()
	out := make([][]byte, len(pcm)/frameSize)
	for i := range out {
		out[i] = make([]byte, maxPacket)
		n, err := e.Encode(pcm[i*frameSize:(i+1)*frameSize], out[i])
		if err != nil {
			return nil, err
		}
		if n <= 0 || n > maxPacket {
			return nil, fmt.Errorf("invalid packet size %d", n)
		}
		if cbr && n != bitrate/400 {
			return nil, fmt.Errorf("CBR size %d; expected %d", n, bitrate/400)
		}
		out[i] = out[i][:n]
	}
	return out, nil
}
func decodeAll(c codec, packets [][]byte) ([]float32, error) {
	d, err := c.decoder()
	if err != nil {
		return nil, err
	}
	defer d.Close()
	pcm := make([]float32, len(packets)*frameSize)
	for i, p := range packets {
		n, err := d.Decode(p, pcm[i*frameSize:(i+1)*frameSize])
		if err != nil {
			return nil, err
		}
		if n != frameSize {
			return nil, fmt.Errorf("wrong frame length %d", n)
		}
	}
	var energy float64
	for _, v := range pcm {
		if !finite(float64(v)) || math.Abs(float64(v)) > 8 {
			return nil, fmt.Errorf("nonfinite or excessive decoded amplitude")
		}
		energy += float64(v) * float64(v)
	}
	if energy == 0 {
		return nil, fmt.Errorf("decoder produced only silence")
	}
	return pcm, nil
}
func nrmse(pcm, reference []float32) float64 {
	var errorSum, energy float64
	for i, v := range pcm {
		delta := float64(v) - float64(reference[i])
		errorSum += delta * delta
		energy += float64(reference[i]) * float64(reference[i])
	}
	return math.Sqrt(errorSum / math.Max(energy, 1e-30))
}

func percentile(sorted []int64, q float64) float64 {
	i := int(math.Ceil(q*float64(len(sorted)))) - 1
	if i < 0 {
		i = 0
	}
	return float64(sorted[i]) / 1000
}

func measure(c codec, operation string, streams, repeat, bitrate, complexity int, cbr bool, pcm []float32, reference [][]byte) (r result) {
	r = result{Codec: c.name, Operation: operation, Streams: streams, Repeat: repeat, Bitrate: bitrate, Ticks: len(pcm)/frameSize - warmFrames}
	var e encoder
	decoders := make([]decoder, streams)
	if operation == "encode" {
		var err error
		e, err = c.encoder(bitrate, complexity, cbr)
		if err != nil {
			r.Error = err.Error()
			return
		}
		defer e.Close()
	} else if operation == "decode" {
		defer func() {
			for _, d := range decoders {
				if d != nil {
					d.Close()
				}
			}
		}()
		for i := range decoders {
			var err error
			decoders[i], err = c.decoder()
			if err != nil {
				r.Error = err.Error()
				return
			}
		}
	}
	packet := make([]byte, maxPacket)
	output := make([]float32, frameSize)
	durations := make([]int64, r.Ticks)
	step := func(frame int) (int, error) {
		switch operation {
		case "encode":
			n, err := e.Encode(pcm[frame*frameSize:(frame+1)*frameSize], packet)
			if err == nil {
				r.Checksum += float64(packet[0])
			}
			return n, err
		case "decode":
			for _, d := range decoders {
				n, err := d.Decode(reference[frame], output)
				if err != nil {
					return 0, err
				}
				if n != frameSize {
					return 0, fmt.Errorf("decode frame size %d", n)
				}
				r.Checksum += float64(output[(frame*17)%frameSize])
			}
		case "bridge":
			bridgeNoop()
		}
		return 0, nil
	}
	runtime.GC()
	for i := 0; i < warmFrames; i++ {
		if _, err := step(i); err != nil {
			r.Error = err.Error()
			return
		}
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	cpuStart := cpuTime()
	started := time.Now()
	totalBytes := 0
	for i := 0; i < r.Ticks; i++ {
		tick := time.Now()
		n, err := step(i + warmFrames)
		durations[i] = time.Since(tick).Nanoseconds()
		if err != nil {
			r.Error = err.Error()
			return
		}
		totalBytes += n
	}
	elapsed := time.Since(started)
	cpuElapsed := cpuTime() - cpuStart
	runtime.ReadMemStats(&after)
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	r.WallUS = float64(elapsed.Nanoseconds()) / float64(r.Ticks) / 1000
	r.CPUUS = float64(cpuElapsed) / float64(r.Ticks) / 1000
	r.P50US = percentile(durations, .5)
	r.P99US = percentile(durations, .99)
	r.P999US = percentile(durations, .999)
	r.MaxUS = percentile(durations, 1)
	r.GoAllocs = float64(after.Mallocs-before.Mallocs) / float64(r.Ticks)
	r.ActualKbps = float64(totalBytes) * 8 / (float64(r.Ticks) * .02) / 1000
	return
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0644)
}
func save(out string, results []result, checks []interop, qualities []quality) error {
	if err := writeJSON(filepath.Join(out, "results.json"), results); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(out, "interop.json"), checks); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(out, "quality.json"), qualities); err != nil {
		return err
	}
	f, err := os.Create(filepath.Join(out, "results.csv"))
	if err != nil {
		return err
	}
	w := csv.NewWriter(f)
	_ = w.Write([]string{"codec", "operation", "bitrate", "streams", "repeat", "ticks", "wall_us_tick", "cpu_us_tick", "cpu_one_core_percent", "p50_us", "p99_us", "p999_us", "max_us", "go_allocs_tick", "actual_kbps", "error"})
	number := func(x float64) string { return strconv.FormatFloat(x, 'f', 3, 64) }
	for _, r := range results {
		_ = w.Write([]string{r.Codec, r.Operation, strconv.Itoa(r.Bitrate), strconv.Itoa(r.Streams), strconv.Itoa(r.Repeat), strconv.Itoa(r.Ticks), number(r.WallUS), number(r.CPUUS), number(r.CPUUS / 200), number(r.P50US), number(r.P99US), number(r.P999US), number(r.MaxUS), number(r.GoAllocs), number(r.ActualKbps), r.Error})
	}
	w.Flush()
	err = w.Error()
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	var report strings.Builder
	for _, c := range checks {
		if c.Error != "" || c.NRMSE > .05 {
			report.WriteString("**INTEROPERABILITY GATE FAILED: at least one candidate requires investigation. Timing rows below do not qualify it as a replacement.**\n\n")
			break
		}
	}
	report.WriteString("# Opus codec benchmark\n\nMeasured sequentially, 48 kHz mono, 20 ms VoIP. See metadata.json for exact settings.\n\nCPU % is process CPU time normalized to one real-time 20 ms tick (one core = 100%), not observed desktop CPU or GPU use. Decode N uses N independent decoders sequentially on the same libopus packets; no mixing, device, network or packet-loss work.\n\n| Codec | Operation | kbps | Streams | Median mean tick us | Median CPU % | Median p99 tick us |\n|---|---|---:|---:|---:|---:|---:|\n")
	groups := map[string][]result{}
	var keys []string
	for _, r := range results {
		key := fmt.Sprintf("%s/%s/%d/%d", r.Codec, r.Operation, r.Bitrate, r.Streams)
		if _, ok := groups[key]; !ok {
			keys = append(keys, key)
		}
		groups[key] = append(groups[key], r)
	}
	sort.Strings(keys)
	median := func(values []float64) float64 {
		sort.Float64s(values)
		n := len(values)
		if n%2 == 1 {
			return values[n/2]
		}
		return (values[n/2-1] + values[n/2]) / 2
	}
	for _, key := range keys {
		rows := groups[key]
		r := rows[0]
		var wall, cpu, p99 []float64
		var failure string
		for _, x := range rows {
			if x.Error != "" {
				failure = x.Error
				break
			}
			wall = append(wall, x.WallUS)
			cpu = append(cpu, x.CPUUS/200)
			p99 = append(p99, x.P99US)
		}
		if failure != "" {
			fmt.Fprintf(&report, "| %s | %s | %d | %d | FAILED: %s | - | - |\n", r.Codec, r.Operation, r.Bitrate/1000, r.Streams, failure)
			continue
		}
		fmt.Fprintf(&report, "| %s | %s | %d | %d | %.2f | %.3f | %.2f |\n", r.Codec, r.Operation, r.Bitrate/1000, r.Streams, median(wall), median(cpu), median(p99))
	}
	report.WriteString("\n## Interoperability\n\nAll encoders are decoded by every decoder; NRMSE compares the same packets with the libopus decoder. Values above 0.05 are flagged for investigation, not accepted as transparent replacements. This is not a perceptual encoder quality score.\n\n| Encoder | Decoder | kbps | NRMSE vs C | Result |\n|---|---|---:|---:|---|\n")
	for _, v := range checks {
		status := "OK"
		if v.NRMSE > .05 {
			status = "INVESTIGATE"
		}
		if v.Error != "" {
			status = v.Error
		}
		fmt.Fprintf(&report, "| %s | %s | %d | %.6f | %s |\n", v.Encoder, v.Decoder, v.Bitrate/1000, v.NRMSE, status)
	}
	report.WriteString(qualityReport(qualities))
	report.WriteString("\n## Limits\n\n- Default fixture is deterministic synthetic audio, not human speech. Supply real normalized PCM before choosing a codec.\n- Native codecs include one cgo call per frame; the bridge row measures an empty cgo call. Rust adapters also catch panics. These results assess codec replacement from Go, not a full Rust core.\n- Go allocation counts omit native/Rust allocations and are not comparable total-memory figures.\n- Construction, warmup, PCM generation, validation and report I/O are outside timing. GC remains enabled. No concurrent benchmark processes should run.\n- p99/p999 are observed tick times during a throughput loop, not network latency or a paced audio deadline test. Small samples cannot establish rare tails.\n- CBR is the default common rate-control setting; VBR is exploratory because internal mode/quality decisions can differ. Check byte rates and interoperability before interpreting speed.\n- No FEC/PLC/DTX, malformed-packet, mobile power or perceptual-quality qualification is implied.\n")
	return os.WriteFile(filepath.Join(out, "report.md"), []byte(report.String()), 0644)
}

func run() error {
	seconds := flag.Int("seconds", 8, "measured audio seconds; plus 1 second warmup")
	repeats := flag.Int("repeats", 5, "rotating sequential repetitions")
	complexity := flag.Int("complexity", 5, "encoder complexity 0..10")
	cbr := flag.Bool("cbr", true, "common CBR; false enables exploratory VBR")
	input := flag.String("pcm", "", "normalized f32le 48kHz mono, at least seconds+1 long")
	out := flag.String("out", "results", "output directory")
	flag.Parse()
	if *seconds < 1 || *seconds > 600 || *repeats < 1 || *repeats > 100 || *complexity < 0 || *complexity > 10 {
		return fmt.Errorf("invalid benchmark bounds")
	}
	if err := os.MkdirAll(*out, 0755); err != nil {
		return err
	}
	pcm, source, err := loadPCM(*input, (*seconds+1)*50)
	if err != nil {
		return err
	}
	metadata := map[string]any{"created_utc": time.Now().UTC(), "go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH, "gomaxprocs": runtime.GOMAXPROCS(0), "logical_cpus": runtime.NumCPU(), "libopus": nativeVersion(), "gopus": "v0.1.1", "opus-rs": "0.1.33 (be9884654b1fe018baee6859cf4e00b7a11ec9d2)", "rusty-opus": "0.9.1 (c4af6337ef329f4cb2cd6bd48e84fd121d721780)", "sample_rate": rate, "frame_samples": frameSize, "channels": 1, "application": "voip", "complexity": *complexity, "cbr": *cbr, "fec": false, "dtx": false, "warmup_frames": warmFrames, "measured_frames": *seconds * 50, "repeats": *repeats, "fixture": source, "fixture_sha256": pcmHash(pcm)}
	if err := writeJSON(filepath.Join(*out, "metadata.json"), metadata); err != nil {
		return err
	}
	var results []result
	var checks []interop
	var qualities []quality
	results = append(results, measure(codecs[1], "bridge", 1, 0, 0, *complexity, *cbr, pcm, nil))
	for _, bitrate := range []int{20000, 32000, 48000} {
		reference, err := packets(codecs[1], pcm, bitrate, *complexity, *cbr)
		if err != nil {
			return err
		}
		for _, c := range codecs {
			encoded, err := packets(c, pcm, bitrate, *complexity, *cbr)
			if err != nil {
				checks = append(checks, interop{Encoder: c.name, Bitrate: bitrate, Error: err.Error()})
				continue
			}
			oracle, err := decodeAll(codecs[1], encoded)
			if err != nil {
				checks = append(checks, interop{Encoder: c.name, Decoder: "libopus", Bitrate: bitrate, Error: err.Error()})
				continue
			}
			for _, d := range codecs {
				check := interop{Encoder: c.name, Decoder: d.name, Bitrate: bitrate}
				decoded, err := decodeAll(d, encoded)
				if err != nil {
					check.Error = err.Error()
				} else {
					check.NRMSE = nrmse(decoded, oracle)
				}
				checks = append(checks, check)
			}
			qualities = append(qualities, inspectQuality(c, bitrate, pcm, oracle, encoded))
		}
		for repeat := 0; repeat < *repeats; repeat++ {
			for index := range codecs {
				c := codecs[(index+repeat)%len(codecs)]
				fmt.Fprintf(os.Stderr, "bitrate=%d repeat=%d codec=%s\n", bitrate, repeat+1, c.name)
				results = append(results, measure(c, "encode", 1, repeat+1, bitrate, *complexity, *cbr, pcm, reference))
				for _, streams := range []int{1, 4, 8} {
					results = append(results, measure(c, "decode", streams, repeat+1, bitrate, *complexity, *cbr, pcm, reference))
				}
			}
		}
		if err := save(*out, results, checks, qualities); err != nil {
			return err
		}
	}
	fmt.Println(filepath.Join(*out, "report.md"))
	for _, r := range results {
		if r.Error != "" {
			return fmt.Errorf("codec errors recorded; see report")
		}
	}
	for _, c := range checks {
		if c.Error != "" || c.NRMSE > .05 {
			return fmt.Errorf("interoperability gate failed; see report (timings are not an approval to replace the codec)")
		}
	}
	return nil
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
