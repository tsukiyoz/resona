//go:build darwin || linux

package broadcaster

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func processCPU(t *testing.T) float64 {
	t.Helper()
	var r syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &r); err != nil {
		t.Fatal(err)
	}
	return float64(r.Utime.Sec+r.Stime.Sec) + float64(r.Utime.Usec+r.Stime.Usec)/1e6
}

type scenario struct {
	name string
	config
	stagger bool
}

func TestArchitectureExperiment(t *testing.T) {
	if os.Getenv("BROADCAST_EXPERIMENT") != "1" {
		t.Skip("set BROADCAST_EXPERIMENT=1")
	}
	if mode := os.Getenv("BROADCAST_MODE"); mode != "" && mode != "serial" && mode != "stateless" {
		t.Fatal("BROADCAST_MODE must be serial or stateless")
	}
	selectedModels := models
	filterModels := os.Getenv("BROADCAST_MODELS")
	if single := os.Getenv("BROADCAST_MODEL"); single != "" {
		if filterModels != "" {
			t.Fatal("use BROADCAST_MODEL or BROADCAST_MODELS, not both")
		}
		filterModels = single
	}
	if filterModels != "" {
		selectedModels = nil
		seen := make(map[string]bool)
		for _, name := range strings.Split(filterModels, ",") {
			name = strings.TrimSpace(name)
			found := false
			for _, model := range models {
				if name == model {
					found = true
					break
				}
			}
			if !found || seen[name] {
				t.Fatalf("unknown or duplicate model %q", name)
			}
			seen[name] = true
			selectedModels = append(selectedModels, name)
		}
	}
	seconds := 2.0
	if s := os.Getenv("BROADCAST_SECONDS"); s != "" {
		var err error
		seconds, err = strconv.ParseFloat(s, 64)
		if err != nil || seconds < .1 || seconds > 10 {
			t.Fatal("BROADCAST_SECONDS must be 0.1..10")
		}
	}
	rounds := 3
	if s := os.Getenv("BROADCAST_ROUNDS"); s != "" {
		var err error
		rounds, err = strconv.Atoi(s)
		if err != nil || rounds < 1 || rounds > 10 {
			t.Fatal("BROADCAST_ROUNDS must be 1..10")
		}
	}
	out := os.Getenv("BROADCAST_OUTPUT")
	if out == "" {
		t.Fatal("BROADCAST_OUTPUT directory is required")
	}
	if err := os.MkdirAll(out, 0700); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(out, "results.jsonl"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	meta := map[string]any{"go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH, "gomaxprocs": runtime.GOMAXPROCS(0), "seconds": seconds, "rounds": rounds, "profile": os.Getenv("BROADCAST_PROFILE") == "1"}
	b, _ := json.MarshalIndent(meta, "", "  ")
	if err := os.WriteFile(filepath.Join(out, "environment.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	cases := []scenario{
		{"room-10-one", config{Members: 10, Speakers: 1, Capacity: 32, Work: 64, Rate: 50, SlowMember: -1}, true},
		{"room-10-four", config{Members: 10, Speakers: 4, Capacity: 32, Work: 64, Rate: 50, SlowMember: -1}, true},
		{"room-64-burst", config{Members: 64, Speakers: 32, Capacity: 32, Work: 256, Rate: 200, SlowMember: -1}, false},
		{"room-64-heavy", config{Members: 64, Speakers: 32, Capacity: 32, Work: 4096, Rate: 100, SlowMember: -1}, false},
		{"room-10-slow", config{Members: 10, Speakers: 4, Capacity: 32, Work: 64, Rate: 200, SlowMember: 9, SlowDelay: 2 * time.Millisecond}, true},
		{"room-10-slow-expiry", config{Members: 10, Speakers: 4, Capacity: 32, Work: 64, Rate: 200, SlowMember: 9, SlowDelay: 2 * time.Millisecond, MaxAge: 20 * time.Millisecond}, true},
	}
	baseCases := append([]scenario(nil), cases...)
	for i := range cases {
		cases[i].name += "-stateless"
	}
	for _, sc := range baseCases {
		sc.name += "-serial"
		sc.SerialMember = true
		cases = append(cases, sc)
	}
	for round := 0; round < rounds; round++ {
		for _, sc := range cases {
			if mode := os.Getenv("BROADCAST_MODE"); mode != "" && ((mode == "serial") != sc.SerialMember) {
				continue
			}
			if filter := os.Getenv("BROADCAST_SCENARIO"); filter != "" && filter != sc.name {
				continue
			}
			for j := range selectedModels {
				model := selectedModels[(j+round)%len(selectedModels)]
				t.Run(sc.name+"/"+model+"/"+strconv.Itoa(round+1), func(t *testing.T) {
					r := runExperiment(t, sc, model, seconds, out, round+1)
					if err := enc.Encode(r); err != nil {
						t.Fatal(err)
					}
					b, _ := json.Marshal(r)
					t.Log(string(b))
				})
			}
		}
	}
}

func runExperiment(t *testing.T, sc scenario, model string, seconds float64, out string, round int) result {
	t.Helper()
	b := newBroadcaster(model, sc.config)
	sources := make([]*source, sc.Speakers)
	for i := range sources {
		sources[i] = b.source(i)
	}
	var ready, done sync.WaitGroup
	ready.Add(sc.Speakers)
	done.Add(sc.Speakers)
	startSignal := make(chan struct{})
	var start time.Time
	var maxLag atomic.Int64
	interval := time.Second / time.Duration(sc.Rate)
	events := int(seconds * float64(sc.Rate))
	for i, s := range sources {
		go func(id int, s *source) {
			defer done.Done()
			timer := time.NewTimer(time.Hour)
			timer.Stop()
			defer timer.Stop()
			ready.Done()
			<-startSignal
			offset := time.Duration(0)
			if sc.stagger {
				offset = time.Duration(id) * interval / time.Duration(sc.Speakers)
			}
			for event := 0; event < events; event++ {
				at := start.Add(offset + time.Duration(event)*interval)
				if delay := time.Until(at); delay > 0 {
					timer.Reset(delay)
					<-timer.C
				}
				lag := time.Since(at).Nanoseconds()
				for {
					old := maxLag.Load()
					if lag <= old || maxLag.CompareAndSwap(old, lag) {
						break
					}
				}
				s.emit(at)
			}
		}(i, s)
	}
	ready.Wait()
	runtime.GC()
	profile := os.Getenv("BROADCAST_PROFILE") == "1"
	var cpuFile *os.File
	prefix := filepath.Join(out, sc.name+"-"+model+"-"+strconv.Itoa(round))
	if profile {
		var err error
		cpuFile, err = os.Create(prefix + "-cpu.pprof")
		if err != nil {
			t.Fatal(err)
		}
		if err = pprof.StartCPUProfile(cpuFile); err != nil {
			t.Fatal(err)
		}
		runtime.SetMutexProfileFraction(1)
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	cpu0 := processCPU(t)
	start = time.Now()
	close(startSignal)
	done.Wait()
	drainStart := time.Now()
	b.close()
	end := time.Now()
	cpu1 := processCPU(t)
	runtime.ReadMemStats(&after)
	if profile {
		pprof.StopCPUProfile()
		cpuFile.Close()
		runtime.SetMutexProfileFraction(0)
		mf, err := os.Create(prefix + "-mutex.pprof")
		if err != nil {
			t.Fatal(err)
		}
		if err := pprof.Lookup("mutex").WriteTo(mf, 0); err != nil {
			t.Fatal(err)
		}
		mf.Close()
	}
	r, err := b.result()
	if err != nil {
		t.Fatal(err)
	}
	r.Scenario = sc.name
	r.Round = round
	r.Seconds = end.Sub(start).Seconds()
	r.DrainMS = float64(end.Sub(drainStart)) / float64(time.Millisecond)
	r.CPUSeconds = cpu1 - cpu0
	r.CPUPercent = 100 * r.CPUSeconds / r.Seconds
	r.AllocMiB = float64(after.TotalAlloc-before.TotalAlloc) / (1024 * 1024)
	r.MaxInputLagMS = float64(maxLag.Load()) / 1e6
	if want := uint64(sc.Speakers * (sc.Members - 1) * events); r.Offered != want {
		t.Fatalf("offered=%d want=%d", r.Offered, want)
	}
	return r
}
