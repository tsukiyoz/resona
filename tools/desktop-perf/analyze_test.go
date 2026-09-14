package main

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture() string {
	var b strings.Builder
	w := csv.NewWriter(&b)
	_ = w.Write(windowsHeader)
	for _, t := range []int{0, 1, 3} {
		for _, p := range []struct {
			id       int
			group    string
			cpu, mem float64
		}{{1, "baseline", .1, 40}, {2, "resona", .02, 10}, {3, "resona", .03, 20}} {
			_ = w.Write([]string{fmt.Sprint(t), p.group, fmt.Sprint(p.id), "123456", fmt.Sprint(100 + float64(t)*p.cpu), fmt.Sprint(p.mem * 1048576), fmt.Sprint(p.mem * 1048576), fmt.Sprint(t * 1024), "0", "0", "10", "2"})
		}
	}
	w.Flush()
	return b.String()
}

func near(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-7 {
		t.Fatalf("got %g want %g", got, want)
	}
}

func TestGroupTotalsAndDifferences(t *testing.T) {
	r, err := analyze(strings.NewReader(fixture()), nil, "baseline", 0)
	if err != nil {
		t.Fatal(err)
	}
	if r.Seconds != 3 || r.Samples != 3 || len(r.Processes) != 3 {
		t.Fatalf("bad report %+v", r)
	}
	for _, group := range r.Groups {
		if group.Name == "resona" {
			near(t, group.Metrics["cpu_one_core"].Mean, 5)
			near(t, group.Metrics["private_commit"].Mean, 30)
			near(t, group.Metrics["io_read"].Mean, 2)
		}
	}
	for _, d := range r.Differences {
		switch d.Metric {
		case "private_commit":
			near(t, d.MeanDelta, -10)
			near(t, *d.Percent, -25)
		case "cpu_one_core":
			near(t, *d.Percent, -50)
		case "io_write":
			if d.Percent != nil {
				t.Fatal("zero baseline must be N/A")
			}
		}
	}
	if !strings.Contains(r.markdown(), "not resident RAM") {
		t.Fatal("missing measurement caveat")
	}
}

func TestTimeWeightedStats(t *testing.T) {
	s := &series{PID: 1, Points: []point{{0, []float64{0, 10}}, {1, []float64{1, 20}}, {3, []float64{1, 40}}}}
	r := summarize("test", []*series{s}, []metric{{"cpu", "%", true, 100}, {"memory", "bytes", false, 1}})
	near(t, r.Metrics["cpu"].Mean, 100.0/3)
	near(t, r.Metrics["cpu"].P95, 100)
	near(t, r.Metrics["memory"].Mean, 25)
	near(t, *r.Metrics["memory"].EndMinusStart, 30)
}

func TestRejectCorruptOrIncomparableCSV(t *testing.T) {
	base := fixture()
	for name, input := range map[string]string{
		"nan":           strings.Replace(base, "100,", "NaN,", 1),
		"negative":      strings.Replace(base, "100,", "-1,", 1),
		"counter reset": strings.Replace(base, "100.1,", "99,", 1),
		"identity":      strings.Replace(base, "1,baseline,1,123456", "1,baseline,1,654321", 1),
		"group":         strings.Replace(base, "1,baseline,1", "1,other,1", 1),
		"timestamp":     strings.Replace(base, "1,baseline,1", "0,baseline,1", 1),
		"missing row":   strings.Join(strings.Split(strings.TrimSpace(base), "\n")[:9], "\n"),
		"unaligned":     strings.Replace(base, "1,baseline,1", "1.2,baseline,1", 1),
		"truncated row": base + "4,resona,3\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := analyze(strings.NewReader(input), nil, "baseline", 0); err == nil {
				t.Fatal("accepted invalid CSV")
			}
		})
	}
	for _, skip := range []float64{-1, math.NaN(), math.Inf(1), 3} {
		if _, err := analyze(strings.NewReader(base), nil, "baseline", skip); err == nil {
			t.Fatal("accepted invalid trim")
		}
	}
	if _, err := analyze(strings.NewReader(base), nil, "missing", 0); err == nil {
		t.Fatal("accepted missing baseline")
	}
}

func TestLegacyMacAndTrim(t *testing.T) {
	input := strings.Join(macHeader, ",") + "\n10,7,1000000000,1048576,2097152,0,20\n11,7,1010000000,2097152,2097152,0,22\n13,7,1030000000,2097152,2097152,0,26\n"
	r, err := analyze(strings.NewReader(input), groups{"mac": {7}}, "mac", 1)
	if err != nil {
		t.Fatal(err)
	}
	near(t, r.Groups[0].Metrics["cpu_one_core"].Mean, 1)
	near(t, r.Groups[0].Metrics["interrupt_wakeups"].Mean, 2)
	if r.Samples != 2 || r.Seconds != 2 {
		t.Fatal("trim failed")
	}
	if _, err := analyze(strings.NewReader(input), groups{"mac": {7, 8}}, "mac", 0); err == nil {
		t.Fatal("missing selected PID accepted")
	}
}

func TestCLIReportAndNoOverwrite(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "samples.csv")
	if err := os.WriteFile(input, []byte(fixture()), 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"analyze", "--input", input, "--out", filepath.Join(dir, "report"), "--baseline", "baseline"}
	if err := run(context.Background(), args, io.Discard); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"report.md", "report.json"} {
		if _, err := os.Stat(filepath.Join(dir, "report", name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := run(context.Background(), args, io.Discard); err == nil {
		t.Fatal("overwrote output directory")
	}
	if err := os.WriteFile(filepath.Join(dir, "capture.json"), []byte(`{"status":"failed","error":"process exited"}`), 0600); err != nil {
		t.Fatal(err)
	}
	args[4] = filepath.Join(dir, "failed-report")
	if err := run(context.Background(), args, io.Discard); err == nil || !strings.Contains(err.Error(), "partial captures") {
		t.Fatalf("expected incomplete capture error, got %v", err)
	}
}

func TestMetadataRejectsTruncatedCapture(t *testing.T) {
	r, err := analyze(strings.NewReader(fixture()), nil, "baseline", 0)
	if err != nil {
		t.Fatal(err)
	}
	m := captureMeta{Schema: 1, Samples: 3, Processes: []processInfo{{PID: 1, Group: "baseline", Creation: "123456"}, {PID: 2, Group: "resona", Creation: "123456"}, {PID: 3, Group: "resona", Creation: "123456"}}}
	if err := r.validateMeta(m); err != nil {
		t.Fatal(err)
	}
	m.Samples = 4
	if err := r.validateMeta(m); err == nil {
		t.Fatal("truncation accepted")
	}
}

func TestProcessTrees(t *testing.T) {
	ps := []processInfo{{PID: 1}, {PID: 2, Parent: 1}, {PID: 3, Parent: 2}, {PID: 4}}
	r, err := resolveGroups(ps, groups{"resona": {1}, "ts": {4}}, true)
	if err != nil || len(r) != 4 {
		t.Fatalf("tree: %v %v", r, err)
	}
	if _, err := resolveGroups(ps, groups{"resona": {1}, "ts": {3}}, true); err == nil {
		t.Fatal("overlap accepted")
	}
	if _, err := resolveGroups(ps, groups{"missing": {99}}, true); err == nil {
		t.Fatal("missing root accepted")
	}
	r, err = resolveGroups(ps, groups{"only": {1}}, false)
	if err != nil || len(r) != 1 {
		t.Fatal("explicit selection failed")
	}
}

func TestGroupFlags(t *testing.T) {
	g := groups{}
	if err := g.Set("resona=1,2"); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"resona=3", "bad name=4", "other=2", "a=0", "a=7,7", "a=4294967296"} {
		if err := g.Set(s); err == nil {
			t.Fatalf("accepted %s", s)
		}
	}
}
