package server

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/metrics"
	"runtime/pprof"
	"runtime/trace"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/tsukiyoz/resona/internal/audio"
	"github.com/tsukiyoz/resona/internal/client"
	w "github.com/tsukiyoz/resona/internal/nativewire"
	"github.com/tsukiyoz/resona/internal/noiseudp"
	"github.com/tsukiyoz/resona/internal/protocol/native"
)

// Diagnostic-only wrappers: stamp a reserved field of synthetic payloads on
// receipt, and measure through encryption/socket submission. Product packets
// and production server code are untouched. Counters allocate no per-packet data.
type probeHistogram struct {
	bins           [5001]atomic.Uint64 // 10 us buckets; final bucket is >=50 ms.
	max, windowMax atomic.Int64
	count          atomic.Uint64
}

func atomicMax(dst *atomic.Int64, n int64) {
	for old := dst.Load(); n > old; old = dst.Load() {
		if dst.CompareAndSwap(old, n) {
			return
		}
	}
}
func (h *probeHistogram) add(d time.Duration) {
	if d < 0 {
		return
	}
	h.bins[min(int(d/(10*time.Microsecond)), 5000)].Add(1)
	h.count.Add(1)
	atomicMax(&h.max, int64(d))
	atomicMax(&h.windowMax, int64(d))
}
func (h *probeHistogram) summary() map[string]any {
	n := h.count.Load()
	r := map[string]any{"count": n, "max_ms": float64(h.max.Load()) / 1e6, "over_50ms_count": h.bins[5000].Load()}
	for _, q := range []struct {
		name     string
		fraction float64
	}{{"p50_ms", .5}, {"p99_ms", .99}, {"p999_ms", .999}} {
		var sum uint64
		for i := range h.bins {
			sum += h.bins[i].Load()
			if n > 0 && sum >= uint64(math.Ceil(float64(n)*q.fraction)) {
				if i == 5000 {
					r[q.name] = nil
				} else {
					r[q.name] = float64(i+1) * .01
				}
				break
			}
		}
	}
	return r
}

type probeConnection struct {
	w.Connection
	origin  time.Time
	h       *probeHistogram
	enabled *atomic.Bool
}

func (c probeConnection) ReceiveDatagram(ctx context.Context) ([]byte, error) {
	b, e := c.Connection.ReceiveDatagram(ctx)
	if e == nil && len(b) == w.ClientVoiceHeader+80 {
		binary.BigEndian.PutUint64(b[w.ClientVoiceHeader+8:], uint64(time.Since(c.origin)))
	}
	return b, e
}
func (c probeConnection) SendDatagram(b []byte) error {
	e := c.Connection.SendDatagram(b)
	if e == nil && c.enabled.Load() && len(b) == w.ServerVoiceHeader+80 {
		c.h.add(time.Since(c.origin) - time.Duration(binary.BigEndian.Uint64(b[w.ServerVoiceHeader+8:])))
	}
	return e
}

type probeListener struct {
	w.Listener
	origin  time.Time
	h       *probeHistogram
	enabled *atomic.Bool
}

func (l probeListener) Accept(ctx context.Context) (w.Connection, error) {
	c, e := l.Listener.Accept(ctx)
	if e != nil {
		return nil, e
	}
	return probeConnection{c, l.origin, l.h, l.enabled}, nil
}
func probeCPU(t *testing.T) float64 {
	t.Helper()
	var r syscall.Rusage
	if e := syscall.Getrusage(syscall.RUSAGE_SELF, &r); e != nil {
		t.Fatal(e)
	}
	return float64(r.Utime.Sec+r.Stime.Sec) + float64(r.Utime.Usec+r.Stime.Usec)/1e6
}
func probeMetrics() map[string]float64 {
	names := []string{"/gc/cycles/total:gc-cycles", "/gc/heap/allocs:bytes", "/memory/classes/heap/objects:bytes", "/cpu/classes/gc/mark/assist:cpu-seconds", "/cpu/classes/gc/total:cpu-seconds"}
	s := make([]metrics.Sample, len(names))
	for i, n := range names {
		s[i].Name = n
	}
	metrics.Read(s)
	r := make(map[string]float64, len(s))
	for _, v := range s {
		if v.Value.Kind() == metrics.KindUint64 {
			r[v.Name] = float64(v.Value.Uint64())
		} else {
			r[v.Name] = v.Value.Float64()
		}
	}
	return r
}

func probePauses() ([]float64, []uint64) {
	s := []metrics.Sample{{Name: "/sched/pauses/total/gc:seconds"}}
	metrics.Read(s)
	h := s[0].Value.Float64Histogram()
	return append([]float64(nil), h.Buckets...), append([]uint64(nil), h.Counts...)
}

// Run only as a child of TestServerGCProbe. stdin is a private local control
// pipe; no profiling port, credential file or user server is used.
func TestGCProbeServer(t *testing.T) {
	if os.Getenv("RESONA_GC_CHILD") != "1" {
		t.Skip("private probe subprocess")
	}
	dir := os.Getenv("RESONA_GC_OUTPUT")
	key, e := noiseudp.GenerateKey()
	if e != nil {
		t.Fatal(e)
	}
	chs := []w.Channel{{ID: 1, Name: "one"}, {ID: 2, Name: "two"}, {ID: 3, Name: "three"}, {ID: 4, Name: "four"}}
	s, e := Listen("127.0.0.1:0", Config{Name: "gc-probe", NoiseKey: key, Channels: chs, MaxClients: 64})
	if e != nil {
		t.Fatal(e)
	}
	var hist probeHistogram
	var enabled atomic.Bool
	s.listener = probeListener{s.listener, time.Now(), &hist, &enabled}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx) }()
	pub, _ := noiseudp.PublicKey(key)
	enc := json.NewEncoder(os.Stdout)
	if e = enc.Encode(client.ServerProfile{Protocol: "resona-noise", Address: s.Addr().String(), ServerPublicKey: hex.EncodeToString(pub)}); e != nil {
		t.Fatal(e)
	}
	scan := bufio.NewScanner(os.Stdin)
	if !scan.Scan() || scan.Text() != "start" {
		t.Fatal("missing start")
	}
	// Remove bootstrap garbage before every mode, including the bounded GC-off
	// diagnostic. This forced collection is outside all measured intervals.
	runtime.GC()
	var cpuFile, traceFile *os.File
	if os.Getenv("RESONA_GC_DIAGNOSTIC") == "1" {
		cpuFile, e = os.Create(filepath.Join(dir, "cpu.pprof"))
		if e != nil {
			t.Fatal(e)
		}
		if e = pprof.StartCPUProfile(cpuFile); e != nil {
			t.Fatal(e)
		}
		traceFile, e = os.Create(filepath.Join(dir, "trace.out"))
		if e != nil {
			t.Fatal(e)
		}
		if e = trace.Start(traceFile); e != nil {
			t.Fatal(e)
		}
	}
	windowFile, e := os.Create(filepath.Join(dir, "windows.jsonl"))
	if e != nil {
		t.Fatal(e)
	}
	defer windowFile.Close()
	windowBuffer := bufio.NewWriterSize(windowFile, 65536)
	windowEncoder := json.NewEncoder(windowBuffer)
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	base := probeMetrics()
	pauseBounds, pauseBefore := probePauses()
	cpu0 := probeCPU(t)
	start := time.Now()
	enabled.Store(true)
	if e = enc.Encode(map[string]bool{"started": true}); e != nil {
		t.Fatal(e)
	}
	stopSample := make(chan struct{})
	sampleDone := make(chan struct{})
	var windowErr error
	go func() {
		defer close(sampleDone)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		last := base
		for {
			select {
			case <-stopSample:
				return
			case <-ticker.C:
				next := probeMetrics()
				windowErr = windowEncoder.Encode(map[string]float64{"seconds": time.Since(start).Seconds(), "max_forward_ms": float64(hist.windowMax.Swap(0)) / 1e6, "gc_cycles": next["/gc/cycles/total:gc-cycles"] - last["/gc/cycles/total:gc-cycles"], "assist_ms": 1000 * (next["/cpu/classes/gc/mark/assist:cpu-seconds"] - last["/cpu/classes/gc/mark/assist:cpu-seconds"]), "heap_mib": next["/memory/classes/heap/objects:bytes"] / (1 << 20)})
				if windowErr != nil {
					return
				}
				last = next
			}
		}
	}()
	if !scan.Scan() || scan.Text() != "stop" {
		t.Fatal("missing stop")
	}
	enabled.Store(false)
	elapsed := time.Since(start).Seconds()
	cpu := probeCPU(t) - cpu0
	close(stopSample)
	<-sampleDone
	if windowErr != nil {
		t.Fatal(windowErr)
	}
	if e = windowBuffer.Flush(); e != nil {
		t.Fatal(e)
	}
	end := probeMetrics()
	_, pauseAfter := probePauses()
	for i := range pauseAfter {
		pauseAfter[i] -= pauseBefore[i]
	}
	runtime.ReadMemStats(&after)
	var maxPause uint64
	var pauses []uint64
	for i := before.NumGC; i < after.NumGC; i++ {
		if after.NumGC-i > 256 {
			continue
		}
		n := after.PauseNs[i%256]
		pauses = append(pauses, n)
		maxPause = max(maxPause, n)
	}
	result := map[string]any{"seconds": elapsed, "cpu_percent_one_core": cpu / elapsed * 100, "alloc_mib_per_second": float64(after.TotalAlloc-before.TotalAlloc) / (1 << 20) / elapsed, "heap_end_mib": float64(after.HeapAlloc) / (1 << 20), "gc_cycles": after.NumGC - before.NumGC, "gc_pause_total_ms": float64(after.PauseTotalNs-before.PauseTotalNs) / 1e6, "gc_pause_max_last256_ms": float64(maxPause) / 1e6, "gc_pause_last256_ns": pauses, "gc_assist_ms": 1000 * (end["/cpu/classes/gc/mark/assist:cpu-seconds"] - base["/cpu/classes/gc/mark/assist:cpu-seconds"]), "gc_cpu_ms": 1000 * (end["/cpu/classes/gc/total:cpu-seconds"] - base["/cpu/classes/gc/total:cpu-seconds"]), "forward": hist.summary()}
	var pauseCount uint64
	for _, n := range pauseAfter {
		pauseCount += n
	}
	var pauseCumulative uint64
	for i, n := range pauseAfter {
		pauseCumulative += n
		if n > 0 && !math.IsInf(pauseBounds[i+1], 0) {
			result["gc_single_pause_max_bucket_upper_ms"] = pauseBounds[i+1] * 1000
			if _, ok := result["gc_single_pause_p99_bucket_upper_ms"]; !ok && pauseCumulative >= uint64(math.Ceil(float64(pauseCount)*.99)) {
				result["gc_single_pause_p99_bucket_upper_ms"] = pauseBounds[i+1] * 1000
			}
		}
	}
	if traceFile != nil {
		trace.Stop()
		traceFile.Close()
		pprof.StopCPUProfile()
		cpuFile.Close()
		f, e := os.Create(filepath.Join(dir, "allocs.pprof"))
		if e != nil {
			t.Fatal(e)
		}
		if e = pprof.Lookup("allocs").WriteTo(f, 0); e != nil {
			t.Fatal(e)
		}
		f.Close()
	}
	cancel()
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	if e = enc.Encode(result); e != nil {
		t.Fatal(e)
	}
}

func probeInt(t *testing.T, name string, def, low, high int) int {
	t.Helper()
	s := os.Getenv(name)
	if s == "" {
		return def
	}
	n, e := strconv.Atoi(s)
	if e != nil || n < low || n > high {
		t.Fatalf("invalid %s", name)
	}
	return n
}

// Separate server and load generator processes. GOGC affects only the child.
// Run without -race for measurements; diagnostic profiles are separate runs.
func TestServerGCProbe(t *testing.T) {
	if os.Getenv("RESONA_SERVER_GC_PROBE") != "1" {
		t.Skip("set RESONA_SERVER_GC_PROBE=1")
	}
	members := probeInt(t, "RESONA_GC_MEMBERS", 16, 4, 64)
	rooms := probeInt(t, "RESONA_GC_ROOMS", 1, 1, 4)
	speakers := probeInt(t, "RESONA_GC_SPEAKERS_PER_ROOM", 4, 1, 4)
	seconds := probeInt(t, "RESONA_GC_SECONDS", 30, 1, 1800)
	if members%rooms != 0 || members/rooms <= speakers {
		t.Fatal("need equal rooms with at least one listener")
	}
	gc := os.Getenv("RESONA_GC_PERCENT")
	if gc == "" {
		gc = "100"
	}
	if gc != "100" && gc != "400" && gc != "off" {
		t.Fatal("unsupported diagnostic GOGC")
	}
	if gc == "off" && seconds > 45 {
		t.Fatal("GC-off diagnostic is limited to 45 seconds")
	}
	dir := os.Getenv("RESONA_GC_OUTPUT")
	if dir == "" {
		t.Fatal("set RESONA_GC_OUTPUT to an artifact directory")
	}
	if e := os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	self, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	cmd := exec.Command(self, "-test.run=^TestGCProbeServer$", "-test.timeout=35m")
	cmd.Env = append(os.Environ(), "RESONA_GC_CHILD=1", "GOGC="+gc, "GOMEMLIMIT=256MiB")
	stdin, e := cmd.StdinPipe()
	if e != nil {
		t.Fatal(e)
	}
	stdout, e := cmd.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	stderr, e := os.Create(filepath.Join(dir, "server.stderr"))
	if e != nil {
		t.Fatal(e)
	}
	defer stderr.Close()
	cmd.Stderr = stderr
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	waited := false
	t.Cleanup(func() {
		stdin.Close()
		if !waited {
			cmd.Process.Kill()
			cmd.Wait()
		}
	})
	dec := json.NewDecoder(bufio.NewReader(stdout))
	var profile client.ServerProfile
	if e = dec.Decode(&profile); e != nil {
		t.Fatal(e)
	}
	var remotes []client.RemoteConnection
	var transports []audio.Transport
	var received atomic.Uint64
	var stableReceived atomic.Uint64
	var e2e probeHistogram
	var measure atomic.Bool
	churn := os.Getenv("RESONA_GC_CHURN") == "1"
	connect := func(i int) (client.RemoteConnection, audio.Transport, error) {
		p := profile
		p.Nickname = fmt.Sprintf("gc-probe-%d", i)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		c, e := (native.Connector{}).Connect(ctx, p, "", func(client.RemoteState) {})
		if e != nil {
			return nil, nil, e
		}
		t.Cleanup(func() { c.Close() })
		if room := i/(members/rooms) + 1; room != 1 {
			if e = c.MoveChannel(ctx, strconv.Itoa(room)); e != nil {
				c.Close()
				return nil, nil, e
			}
		}
		a := c.(audio.Transport)
		a.SetVoiceHandler(func(p audio.Packet) {
			if measure.Load() && len(p.Data) == 80 {
				received.Add(1)
				if !churn || i != members-1 {
					stableReceived.Add(1)
				}
				e2e.add(time.Duration(time.Now().UnixNano() - int64(binary.BigEndian.Uint64(p.Data[:8]))))
			}
		})
		if e = a.SetVoiceMuted(ctx, i%(members/rooms) >= speakers, false); e != nil {
			c.Close()
			return nil, nil, e
		}
		return c, a, nil
	}
	for i := 0; i < members; i++ {
		c, a, err := connect(i)
		if err != nil {
			t.Fatal(err)
		}
		remotes = append(remotes, c)
		transports = append(transports, a)
		time.Sleep(120 * time.Millisecond)
	}
	time.Sleep(time.Second)
	fmt.Fprintln(stdin, "start")
	var started map[string]bool
	if e = dec.Decode(&started); e != nil || !started["started"] {
		t.Fatalf("start: %v", e)
	}
	measure.Store(true)
	var churnWG sync.WaitGroup
	churnStop := make(chan struct{})
	churnError := make(chan error, 1)
	stopChurn := sync.OnceFunc(func() { close(churnStop); churnWG.Wait() })
	defer stopChurn()
	var churnCount atomic.Uint64
	// Reconnect only a listening member. Expected steady-listener delivery counts
	// exclude this member during churn because membership changes asynchronously.
	if churn {
		churnWG.Add(1)
		go func() {
			defer churnWG.Done()
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-churnStop:
					return
				case <-ticker.C:
					remotes[members-1].Close()
					// Close is local; allow the remote worker to release its admission
					// slot before replacing a member at the 64-connection limit.
					select {
					case <-churnStop:
						return
					case <-time.After(150 * time.Millisecond):
					}
					c, _, err := connect(members - 1)
					if err != nil {
						churnError <- err
						return
					}
					remotes[members-1] = c
					churnCount.Add(1)
				}
			}
		}()
	}
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	attempts := 0
	for frame := 0; frame < seconds*50; frame++ {
		<-ticker.C
		select {
		case err := <-churnError:
			t.Fatal(err)
		default:
		}
		for i, a := range transports {
			if i%(members/rooms) >= speakers {
				continue
			}
			var data [80]byte
			binary.BigEndian.PutUint64(data[:8], uint64(time.Now().UnixNano()))
			if e = a.SendVoice(data[:], audio.CodecOpusVoice); e != nil {
				t.Fatal(e)
			}
			attempts++
		}
		if frame > 0 && frame%1500 == 0 {
			t.Logf("progress: %.0fs, sent=%d received=%d", float64(frame)/50, attempts, received.Load())
		}
	}
	ticker.Stop()
	stopChurn()
	select {
	case err := <-churnError:
		t.Fatal(err)
	default:
	}
	time.Sleep(200 * time.Millisecond)
	measure.Store(false)
	fmt.Fprintln(stdin, "stop")
	var result map[string]any
	if e = dec.Decode(&result); e != nil {
		t.Fatal(e)
	}
	stdin.Close()
	e = cmd.Wait()
	waited = true
	if e != nil {
		t.Fatal(e)
	}
	result["gogc"] = gc
	result["members"] = members
	result["rooms"] = rooms
	result["speakers_per_room"] = speakers
	result["churn_reconnects"] = churnCount.Load()
	result["sent_voice"] = attempts
	result["received_voice"] = received.Load()
	result["expected_without_churn"] = attempts * (members/rooms - 1)
	expectedStable := attempts * (members/rooms - 1)
	if churn {
		expectedStable -= seconds * 50 * speakers
	}
	result["expected_stable_deliveries"] = expectedStable
	result["received_stable_deliveries"] = stableReceived.Load()
	result["end_to_end"] = e2e.summary()
	result["diagnostic"] = os.Getenv("RESONA_GC_DIAGNOSTIC") == "1"
	b, e := json.MarshalIndent(result, "", "  ")
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(dir, "result.json"), b, 0600); e != nil {
		t.Fatal(e)
	}
	delete(result, "windows")
	delete(result, "gc_pause_last256_ns")
	b, _ = json.Marshal(result)
	t.Logf("GC_PROBE %s", b)
}
