package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestNativeProcessMetrics(t *testing.T) {
	r, err := openProcess(uint32(os.Getpid()))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(r.handle)
	values, err := r.read()
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 7 {
		t.Fatalf("unexpected fields: %v", values)
	}
	for _, i := range []int{1, 2, 6} {
		v, err := number(values[i])
		if err != nil || v <= 0 {
			t.Fatalf("invalid native metric %d: %s", i, values[i])
		}
	}
}

func TestPerfChildHelper(t *testing.T) {
	if os.Getenv("RESONA_PERF_TEST_CHILD") != "1" {
		return
	}
	_, _ = os.Stdout.WriteString("ready\n")
	_, _ = io.Copy(io.Discard, os.Stdin)
	os.Exit(0)
}

func startChild(t *testing.T) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestPerfChildHelper$")
	cmd.Env = append(os.Environ(), "RESONA_PERF_TEST_CHILD=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stdin.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() })
	ready := make(chan error, 1)
	go func() {
		s, err := bufio.NewReader(stdout).ReadString('\n')
		if err == nil && s != "ready\n" {
			err = io.ErrUnexpectedEOF
		}
		ready <- err
	}()
	select {
	case err := <-ready:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("child startup timeout")
	}
	return cmd
}

func TestWindowsCaptureRoundTrip(t *testing.T) {
	cmd := startChild(t)
	dir := filepath.Join(t.TempDir(), "capture")
	o := collectOptions{Groups: groups{"test": {uint32(cmd.Process.Pid)}}, Output: dir, Scenario: "CI-idle-helper", Duration: time.Second, Interval: 250 * time.Millisecond, Children: true}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := collect(ctx, o, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := run(ctx, []string{"analyze", "--input", filepath.Join(dir, "samples.csv"), "--out", dir + "-report"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "capture.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m captureMeta
	if err = json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if m.Status != "complete" || m.Samples < 2 {
		t.Fatalf("invalid capture: %+v", m)
	}
}

func TestExitedProcessFailsAndCancellationMarked(t *testing.T) {
	cmd := startChild(t)
	r, err := openProcess(uint32(cmd.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(r.handle)
	if err = cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if _, err = windows.WaitForSingleObject(r.handle, 5000); err != nil {
		t.Fatal(err)
	}
	if _, err = r.read(); err == nil {
		t.Fatal("exited process returned metrics")
	}
	dir := filepath.Join(t.TempDir(), "cancelled")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = collect(ctx, collectOptions{Output: dir, Warmup: time.Second}, io.Discard)
	if err == nil {
		t.Fatal("cancelled collection succeeded")
	}
	data, err := os.ReadFile(filepath.Join(dir, "capture.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m captureMeta
	if err = json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if m.Status != "failed" {
		t.Fatal("partial capture not marked failed")
	}
}
