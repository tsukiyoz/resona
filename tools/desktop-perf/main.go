// desktop-perf collects Windows process groups and analyzes Windows/macOS CSV.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type groups map[string][]uint32

func (g groups) String() string { return fmt.Sprint(map[string][]uint32(g)) }
func (g groups) Set(s string) error {
	name, values, ok := strings.Cut(s, "=")
	if !ok || !regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]{0,39}$`).MatchString(name) {
		return errors.New("group must be label=PID,PID (ASCII letters, digits, underscore or hyphen)")
	}
	if _, ok := g[name]; ok {
		return fmt.Errorf("duplicate group %s", name)
	}
	var ids []uint32
	for _, s := range strings.Split(values, ",") {
		n, err := strconv.ParseUint(s, 10, 32)
		if err != nil || n == 0 {
			return fmt.Errorf("invalid PID %q", s)
		}
		for _, existing := range g {
			for _, id := range existing {
				if id == uint32(n) {
					return fmt.Errorf("PID %d belongs to multiple groups", n)
				}
			}
		}
		for _, id := range ids {
			if id == uint32(n) {
				return fmt.Errorf("duplicate PID %d", n)
			}
		}
		ids = append(ids, uint32(n))
	}
	g[name] = ids
	return nil
}

type collectOptions struct {
	Groups                     groups
	Output, Scenario           string
	Duration, Interval, Warmup time.Duration
	Children                   bool
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "desktop-perf:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		fmt.Fprintln(out, `desktop-perf compare
desktop-perf list
desktop-perf collect --group baseline=1234 --group resona=5678 --duration 120s --warmup 10s --scenario offline-minimized --out capture
desktop-perf analyze --input capture/samples.csv --baseline baseline --out report
For old macOS CSV, add --group baseline=1234 --group resona=5678,5679 to analyze.
Capture/report directories must be new. Collect is Windows-only; analyze is cross-platform.`)
		return nil
	}
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	fs.SetOutput(out)
	g := groups{}
	switch args[0] {
	case "compare":
		if len(args) != 1 {
			return errors.New("compare takes no arguments; use collect for custom settings")
		}
		return compare(ctx, os.Stdin, out)
	case "list":
		filter := fs.String("filter", "", "comma-separated substrings of process names")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 0 {
			return errors.New("list takes no positional arguments")
		}
		return listProcesses(out, *filter)
	case "collect":
		o := collectOptions{Groups: g}
		fs.Var(g, "group", "repeat label=PID,PID; descendants included by default")
		fs.StringVar(&o.Output, "out", "", "new output directory")
		fs.StringVar(&o.Scenario, "scenario", "", "workload description (no secrets)")
		fs.DurationVar(&o.Duration, "duration", 120*time.Second, "measurement duration")
		fs.DurationVar(&o.Interval, "interval", time.Second, "sampling interval, minimum 250ms")
		fs.DurationVar(&o.Warmup, "warmup", 10*time.Second, "time to minimize windows before sampling")
		fs.BoolVar(&o.Children, "children", true, "include descendants present at start; fail on observed new descendants")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 0 || len(g) == 0 || o.Output == "" || o.Scenario == "" {
			return errors.New("collect requires --group, --scenario, --out and no positional arguments")
		}
		if o.Interval < 250*time.Millisecond || o.Duration < o.Interval || o.Duration > 24*time.Hour || o.Warmup < 0 || o.Warmup > time.Hour {
			return errors.New("require 250ms <= interval <= duration <= 24h, and 0 <= warmup <= 1h")
		}
		return collect(ctx, o, out)
	case "analyze":
		input := fs.String("input", "", "Windows or legacy macOS CSV")
		dir := fs.String("out", "", "new report directory")
		baseline := fs.String("baseline", "", "reference group for signed differences")
		skip := fs.Float64("skip", 0, "discard samples before this many seconds from first sample")
		fs.Var(g, "group", "legacy macOS only: repeat label=PID,PID")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 0 || *input == "" || *dir == "" {
			return errors.New("analyze requires --input and --out")
		}
		f, err := os.Open(*input)
		if err != nil {
			return err
		}
		defer f.Close()
		r, err := analyze(f, g, *baseline, *skip)
		if err != nil {
			return err
		}
		r.Source = filepath.Base(*input)
		if r.Platform == "windows" {
			data, err := os.ReadFile(filepath.Join(filepath.Dir(*input), "capture.json"))
			if err == nil {
				var meta captureMeta
				if err = json.Unmarshal(data, &meta); err != nil {
					return fmt.Errorf("capture metadata: %w", err)
				}
				if meta.Status != "complete" {
					return fmt.Errorf("capture is %q: %s; partial captures are not comparable", meta.Status, meta.Error)
				}
				if err = r.validateMeta(meta); err != nil {
					return err
				}
				r.Capture = &meta
				if meta.MaxSampleSpanSeconds > meta.IntervalSeconds*.25 {
					r.Warnings = append(r.Warnings, "Sampling a process batch took over 25% of the configured interval; reduce the process count or increase --interval. Per-process reads are sequential.")
				}
			} else if !os.IsNotExist(err) {
				return err
			} else {
				r.Warnings = append(r.Warnings, "capture.json missing: completion and environment cannot be verified")
			}
		}
		if err = os.Mkdir(*dir, 0o700); err != nil {
			return err
		}
		data, err := json.MarshalIndent(r, "", "  ")
		if err != nil {
			return err
		}
		if err = os.WriteFile(filepath.Join(*dir, "report.json"), append(data, '\n'), 0o600); err != nil {
			return err
		}
		if err = os.WriteFile(filepath.Join(*dir, "report.md"), []byte(r.markdown()), 0o600); err != nil {
			return err
		}
		fmt.Fprint(out, r.markdown())
		fmt.Fprintln(out, "\nReports:", filepath.Join(*dir, "report.md"), filepath.Join(*dir, "report.json"))
		return nil
	default:
		return fmt.Errorf("unknown command %q; use --help", args[0])
	}
}

func (r *report) validateMeta(m captureMeta) error {
	if m.Schema != 1 || m.Samples != r.originalSamples || len(m.Processes) != len(r.roster) {
		return errors.New("capture metadata does not match CSV schema/sample count/process roster")
	}
	seen := map[uint32]bool{}
	for _, p := range m.Processes {
		s := r.roster[p.PID]
		if seen[p.PID] || s == nil || p.Creation != s.Creation || p.Group != s.Group {
			return errors.New("capture metadata does not match CSV process identities")
		}
		seen[p.PID] = true
	}
	return nil
}

type processInfo struct {
	PID      uint32 `json:"pid"`
	Parent   uint32 `json:"parent_pid"`
	Name     string `json:"name"`
	Threads  uint32 `json:"threads"`
	Creation string `json:"creation_id,omitempty"`
	Group    string `json:"group,omitempty"`
}

type captureMeta struct {
	Schema               int           `json:"schema"`
	Status               string        `json:"status"`
	Error                string        `json:"error,omitempty"`
	Scenario             string        `json:"scenario"`
	OS                   string        `json:"os"`
	Arch                 string        `json:"arch"`
	LogicalCPUs          int           `json:"logical_cpus"`
	Started              string        `json:"started_utc"`
	DurationSeconds      float64       `json:"requested_duration_seconds"`
	IntervalSeconds      float64       `json:"interval_seconds"`
	Samples              int           `json:"samples"`
	Processes            []processInfo `json:"processes"`
	Children             bool          `json:"include_children"`
	MaxSampleSpanSeconds float64       `json:"max_sample_span_seconds"`
}
