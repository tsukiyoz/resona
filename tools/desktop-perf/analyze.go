package main

import (
	"encoding/csv"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
)

var (
	windowsHeader = []string{"elapsed_seconds", "group", "pid", "creation_id", "cpu_seconds", "working_set_bytes", "private_bytes", "read_bytes", "write_bytes", "other_bytes", "handles", "threads"}
	macHeader     = []string{"monotonic_seconds", "pid", "cpu_ns", "physical_bytes", "resident_bytes", "idle_wakeups", "interrupt_wakeups"}
)

type metric struct {
	Name, Unit string
	Counter    bool
	Scale      float64
}

var winMetrics = []metric{
	{"cpu_one_core", "%", true, 100},
	{"working_set_sum", "MiB", false, 1.0 / 1048576},
	{"private_commit", "MiB", false, 1.0 / 1048576},
	{"io_read", "KiB/s", true, 1.0 / 1024},
	{"io_write", "KiB/s", true, 1.0 / 1024},
	{"io_other", "KiB/s", true, 1.0 / 1024},
	{"handles", "count", false, 1},
	{"threads", "count", false, 1},
}

var macMetrics = []metric{
	{"cpu_one_core", "%", true, 100.0 / 1e9},
	{"physical_footprint", "MiB", false, 1.0 / 1048576},
	{"resident_sum", "MiB", false, 1.0 / 1048576},
	{"package_idle_wakeups", "/s", true, 1},
	{"interrupt_wakeups", "/s", true, 1},
}

type point struct {
	Time   float64
	Values []float64
}
type series struct {
	Group, Creation string
	PID             uint32
	Points          []point
}
type stats struct {
	Mean          float64  `json:"mean"`
	P95           float64  `json:"p95"`
	Min           float64  `json:"min"`
	Max           float64  `json:"max"`
	EndMinusStart *float64 `json:"end_minus_start,omitempty"`
}
type groupResult struct {
	Name    string           `json:"name"`
	PIDs    []uint32         `json:"pids"`
	Metrics map[string]stats `json:"metrics"`
}
type difference struct {
	Group     string   `json:"group"`
	Metric    string   `json:"metric"`
	MeanDelta float64  `json:"mean_delta"`
	Percent   *float64 `json:"percent_delta"`
}
type report struct {
	Source          string        `json:"source"`
	Platform        string        `json:"platform"`
	Seconds         float64       `json:"seconds"`
	Samples         int           `json:"samples_per_process"`
	Baseline        string        `json:"baseline"`
	Groups          []groupResult `json:"groups"`
	Processes       []groupResult `json:"processes"`
	Differences     []difference  `json:"differences"`
	Warnings        []string      `json:"warnings"`
	Capture         *captureMeta  `json:"capture,omitempty"`
	specs           []metric
	roster          map[uint32]*series
	originalSamples int
}

func number(s string) (float64, error) {
	n, e := strconv.ParseFloat(s, 64)
	if e != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 {
		return 0, fmt.Errorf("invalid nonnegative finite number %q", s)
	}
	return n, nil
}

func analyze(input io.Reader, selected groups, baseline string, skip float64) (*report, error) {
	if math.IsNaN(skip) || math.IsInf(skip, 0) || skip < 0 {
		return nil, fmt.Errorf("invalid --skip")
	}
	c := csv.NewReader(input)
	header, err := c.Read()
	if err != nil {
		return nil, err
	}
	if len(header) > 0 {
		header[0] = strings.TrimPrefix(header[0], "\ufeff")
	}
	r := &report{Baseline: baseline, Warnings: []string{
		"CPU 100% means one logical core, not the whole machine. P95/max describe sampling intervals, not frame latency.",
		"No GPU, power, audio latency or game FPS measurement. Short-window CPU ratios and memory growth are not proof of user-visible impact or leaks.",
		"Only listed processes are attributed; shared OS services and helpers outside the selected process trees are excluded.",
	}}
	switch strings.Join(header, ",") {
	case strings.Join(windowsHeader, ","):
		r.Platform = "windows"
		r.specs = winMetrics
		if len(selected) > 0 {
			return nil, fmt.Errorf("Windows CSV already defines groups; --group is only for legacy macOS CSV")
		}
		r.Warnings = append(r.Warnings, "Working-set sums can double-count shared pages. Private commit is committed private virtual memory, not resident RAM or macOS physical footprint.", "Process I/O includes file/device/pipe activity; it is not dedicated disk throughput or network bandwidth. Windows wakeups are unavailable.")
	case strings.Join(macHeader, ","):
		r.Platform = "macos"
		r.specs = macMetrics
		if len(selected) == 0 {
			return nil, fmt.Errorf("macOS CSV needs --group label=PID,PID")
		}
		r.Warnings = append(r.Warnings, "Legacy macOS CSV has no process creation IDs or completion metadata; PID reuse and completed collection cannot be verified. Resident sums may double-count shared pages.")
	default:
		return nil, fmt.Errorf("unsupported CSV header")
	}
	data := map[uint32]*series{}
	selectedByPID := map[uint32]string{}
	for name, ids := range selected {
		for _, id := range ids {
			if _, ok := selectedByPID[id]; ok {
				return nil, fmt.Errorf("duplicate selected PID %d", id)
			}
			selectedByPID[id] = name
		}
	}
	for line := 2; ; line++ {
		row, e := c.Read()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, fmt.Errorf("CSV line %d: %w", line, e)
		}
		pidCol, valueCol := 2, 4
		if r.Platform == "macos" {
			pidCol = 1
			valueCol = 2
		}
		id, e := strconv.ParseUint(row[pidCol], 10, 32)
		if e != nil || id == 0 {
			return nil, fmt.Errorf("line %d: invalid PID", line)
		}
		pid := uint32(id)
		name, creation := "", ""
		if r.Platform == "macos" {
			name = selectedByPID[pid]
			if name == "" {
				continue
			}
		} else {
			name, creation = row[1], row[3]
			g := groups{}
			if e := g.Set(name + "=" + row[2]); e != nil {
				return nil, e
			}
			if n, e := strconv.ParseUint(creation, 10, 64); e != nil || n == 0 {
				return nil, fmt.Errorf("line %d: invalid creation ID", line)
			}
		}
		t, e := number(row[0])
		if e != nil {
			return nil, e
		}
		p := point{Time: t}
		for _, v := range row[valueCol:] {
			n, e := number(v)
			if e != nil {
				return nil, fmt.Errorf("line %d: %w", line, e)
			}
			p.Values = append(p.Values, n)
		}
		s := data[pid]
		if s == nil {
			s = &series{Group: name, Creation: creation, PID: pid}
			data[pid] = s
		}
		if s.Group != name || s.Creation != creation {
			return nil, fmt.Errorf("PID %d changed group or identity", pid)
		}
		if len(s.Points) > 0 {
			prev := s.Points[len(s.Points)-1]
			if p.Time <= prev.Time {
				return nil, fmt.Errorf("PID %d duplicate or non-increasing timestamps", pid)
			}
			for j, spec := range r.specs {
				if spec.Counter && p.Values[j] < prev.Values[j] {
					return nil, fmt.Errorf("PID %d: %s counter decreased", pid, spec.Name)
				}
			}
		}
		s.Points = append(s.Points, p)
	}
	for id := range selectedByPID {
		if data[id] == nil {
			return nil, fmt.Errorf("selected PID %d missing", id)
		}
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("no selected samples")
	}
	ids := make([]uint32, 0, len(data))
	for id := range data {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	ref := data[ids[0]].Points
	r.roster = data
	r.originalSamples = len(ref)
	if len(ref) < 2 {
		return nil, fmt.Errorf("need at least two samples per process")
	}
	for _, id := range ids {
		pts := data[id].Points
		if len(pts) != len(ref) {
			return nil, fmt.Errorf("PID %d has missing samples or a different window", id)
		}
		for i, p := range pts {
			if p.Time != ref[i].Time {
				return nil, fmt.Errorf("PID %d has misaligned timestamps", id)
			}
		}
	}
	start := sort.Search(len(ref), func(i int) bool { return ref[i].Time-ref[0].Time >= skip })
	if len(ref)-start < 2 {
		return nil, fmt.Errorf("need two samples after --skip")
	}
	ref = ref[start:]
	r.Samples = len(ref)
	r.Seconds = ref[len(ref)-1].Time - ref[0].Time
	byGroup := map[string][]*series{}
	for _, id := range ids {
		s := data[id]
		s.Points = s.Points[start:]
		byGroup[s.Group] = append(byGroup[s.Group], s)
		r.Processes = append(r.Processes, summarize(fmt.Sprintf("%s/%d", s.Group, id), []*series{s}, r.specs))
	}
	names := make([]string, 0, len(byGroup))
	for n := range byGroup {
		names = append(names, n)
	}
	sort.Strings(names)
	if baseline == "" {
		baseline = names[0]
		r.Baseline = baseline
	}
	if byGroup[baseline] == nil {
		return nil, fmt.Errorf("unknown baseline %q", baseline)
	}
	for _, name := range names {
		r.Groups = append(r.Groups, summarize(name, byGroup[name], r.specs))
	}
	var base groupResult
	for _, g := range r.Groups {
		if g.Name == baseline {
			base = g
		}
	}
	for _, g := range r.Groups {
		if g.Name == baseline {
			continue
		}
		for _, spec := range r.specs {
			b := base.Metrics[spec.Name].Mean
			d := difference{Group: g.Name, Metric: spec.Name, MeanDelta: g.Metrics[spec.Name].Mean - b}
			if b != 0 {
				pct := d.MeanDelta / b * 100
				d.Percent = &pct
			}
			r.Differences = append(r.Differences, d)
		}
	}
	minDT, maxDT := math.Inf(1), 0.0
	for i := 1; i < len(ref); i++ {
		dt := ref[i].Time - ref[i-1].Time
		minDT = math.Min(minDT, dt)
		maxDT = math.Max(maxDT, dt)
	}
	if maxDT > minDT*1.5 {
		r.Warnings = append(r.Warnings, "Irregular sampling intervals detected; averages and rate P95 are time-weighted.")
	}
	return r, nil
}

type weighted struct{ value, weight float64 }

func summarize(name string, list []*series, specs []metric) groupResult {
	g := groupResult{Name: name, Metrics: map[string]stats{}}
	for _, s := range list {
		g.PIDs = append(g.PIDs, s.PID)
	}
	pts := list[0].Points
	duration := pts[len(pts)-1].Time - pts[0].Time
	for j, spec := range specs {
		vals := make([]float64, len(pts))
		for _, s := range list {
			for i, p := range s.Points {
				vals[i] += p.Values[j]
			}
		}
		st := stats{Min: math.Inf(1)}
		var weights []weighted
		for i := 1; i < len(pts); i++ {
			dt := pts[i].Time - pts[i-1].Time
			if spec.Counter {
				v := (vals[i] - vals[i-1]) * spec.Scale / dt
				st.Mean += v * dt
				st.Min = math.Min(st.Min, v)
				st.Max = math.Max(st.Max, v)
				weights = append(weights, weighted{v, dt})
			} else {
				st.Mean += (vals[i] + vals[i-1]) / 2 * spec.Scale * dt
				weights = append(weights, weighted{vals[i-1] * spec.Scale, dt / 2}, weighted{vals[i] * spec.Scale, dt / 2})
			}
		}
		if !spec.Counter {
			for _, v := range vals {
				st.Min = math.Min(st.Min, v*spec.Scale)
				st.Max = math.Max(st.Max, v*spec.Scale)
			}
			d := (vals[len(vals)-1] - vals[0]) * spec.Scale
			st.EndMinusStart = &d
		}
		st.Mean /= duration
		sort.Slice(weights, func(i, j int) bool { return weights[i].value < weights[j].value })
		acc := 0.0
		for _, v := range weights {
			acc += v.weight
			st.P95 = v.value
			if acc >= duration*.95 {
				break
			}
		}
		g.Metrics[spec.Name] = st
	}
	return g
}

func (r *report) markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Desktop process comparison\n\nPlatform: %s. Window: %.3f seconds; %d samples/process. Baseline: %s.\n", r.Platform, r.Seconds, r.Samples, r.Baseline)
	if r.Capture != nil {
		fmt.Fprintf(&b, "\nScenario: %s. OS: %s; arch: %s; logical CPUs: %d.\n", strings.NewReplacer("\n", " ", "\r", " ", "|", "/").Replace(r.Capture.Scenario), r.Capture.OS, r.Capture.Arch, r.Capture.LogicalCPUs)
	}
	fmt.Fprintln(&b, "\n## Application groups\n\n| Group | Metric | Unit | Mean | P95 | Min | Max | End-start |\n| --- | --- | --- | ---: | ---: | ---: | ---: | ---: |")
	for _, g := range r.Groups {
		for _, spec := range r.specs {
			v := g.Metrics[spec.Name]
			delta := "N/A"
			if v.EndMinusStart != nil {
				delta = fmt.Sprintf("%+.4f", *v.EndMinusStart)
			}
			fmt.Fprintf(&b, "| %s | %s | %s | %.4f | %.4f | %.4f | %.4f | %s |\n", g.Name, spec.Name, spec.Unit, v.Mean, v.P95, v.Min, v.Max, delta)
		}
	}
	fmt.Fprintln(&b, "\n## Mean differences\n\nSigned difference = group minus baseline. Negative means lower measured usage, not necessarily better behavior. Zero baseline produces N/A percent.\n\n| Group | Metric | Absolute delta | Percent delta |\n| --- | --- | ---: | ---: |")
	for _, d := range r.Differences {
		pct := "N/A"
		if d.Percent != nil {
			pct = fmt.Sprintf("%+.2f%%", *d.Percent)
		}
		fmt.Fprintf(&b, "| %s | %s | %+.4f | %s |\n", d.Group, d.Metric, d.MeanDelta, pct)
	}
	fmt.Fprintln(&b, "\n## Per-process means\n\n| Group/PID | Metric | Mean | Unit |\n| --- | --- | ---: | --- |")
	for _, p := range r.Processes {
		for _, spec := range r.specs {
			fmt.Fprintf(&b, "| %s | %s | %.4f | %s |\n", p.Name, spec.Name, p.Metrics[spec.Name].Mean, spec.Unit)
		}
	}
	fmt.Fprint(&b, "\n## Measurement limits\n\n")
	for _, w := range r.Warnings {
		fmt.Fprintf(&b, "- %s\n", w)
	}
	fmt.Fprintln(&b, "\nMemory/handle end-start changes are descriptive, not leak detection. Repeat equivalent workloads across independent runs. Gauge means use trapezoidal time weighting; gauge P95 uses endpoint half-interval weights. Counter P95 uses time-weighted interval rates. Peaks between samples can be missed.")
	return b.String()
}
