package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func compare(ctx context.Context, in io.Reader, out io.Writer) error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("interactive collection requires Windows; use analyze on this platform")
	}
	fmt.Fprintln(out, "Start both clients first. Matching process names are listed below. Choose the GUI root PID; existing children are included. Use 'list' for other app names.")
	if err := listProcesses(out, "resona,teamspeak,ts3client"); err != nil {
		return err
	}
	scanner := bufio.NewScanner(in)
	read := func(prompt string) (string, error) {
		fmt.Fprint(out, prompt)
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				return "", err
			}
			return "", io.EOF
		}
		return strings.TrimSpace(scanner.Text()), nil
	}
	g := groups{}
	for _, name := range []string{"teamspeak", "resona"} {
		value, err := read(name + " root PID (or comma-separated PIDs): ")
		if err != nil {
			return err
		}
		if err = g.Set(name + "=" + value); err != nil {
			return err
		}
	}
	scenario, err := read("Scenario, e.g. offline-minimized / connected-listening (required): ")
	if err != nil {
		return err
	}
	if scenario == "" {
		return fmt.Errorf("scenario cannot be empty")
	}
	dir := "perf-" + time.Now().Format("20060102-150405.000")
	o := collectOptions{Groups: g, Output: dir, Scenario: scenario, Duration: 120 * time.Second, Interval: time.Second, Warmup: 10 * time.Second, Children: true}
	if err = collect(ctx, o, out); err != nil {
		return err
	}
	return run(ctx, []string{"analyze", "--input", filepath.Join(dir, "samples.csv"), "--baseline", "teamspeak", "--out", dir + "-report"}, out)
}
