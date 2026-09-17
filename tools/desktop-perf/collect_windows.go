package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	psapi      = windows.NewLazySystemDLL("psapi.dll")
	kernel32   = windows.NewLazySystemDLL("kernel32.dll")
	getMemory  = psapi.NewProc("GetProcessMemoryInfo")
	getIO      = kernel32.NewProc("GetProcessIoCounters")
	getHandles = kernel32.NewProc("GetProcessHandleCount")
)

// Native PROCESS_MEMORY_COUNTERS_EX: SIZE_T fields must follow pointer width.
type memoryCounters struct {
	Size, Faults                                             uint32
	PeakWorkingSet, WorkingSet                               uintptr
	PeakPagedPool, PagedPool, PeakNonPagedPool, NonPagedPool uintptr
	Pagefile, PeakPagefile, PrivateUsage                     uintptr
}

type processReader struct {
	handle   windows.Handle
	creation uint64
}

func processSnapshot() ([]processInfo, error) {
	h, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(h)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	err = windows.Process32First(h, &entry)
	var result []processInfo
	for err == nil {
		result = append(result, processInfo{PID: entry.ProcessID, Parent: entry.ParentProcessID, Name: windows.UTF16ToString(entry.ExeFile[:]), Threads: entry.Threads})
		err = windows.Process32Next(h, &entry)
	}
	if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return nil, err
	}
	return result, nil
}

func listProcesses(out io.Writer, filter string) error {
	ps, err := processSnapshot()
	if err != nil {
		return err
	}
	sort.Slice(ps, func(i, j int) bool { return ps[i].PID < ps[j].PID })
	fmt.Fprintln(out, "PID\tPARENT\tTHREADS\tNAME")
	for _, p := range ps {
		if filter != "" {
			match := false
			for _, part := range strings.Split(strings.ToLower(filter), ",") {
				if part != "" && strings.Contains(strings.ToLower(p.Name), part) {
					match = true
				}
			}
			if !match {
				continue
			}
		}
		fmt.Fprintf(out, "%d\t%d\t%d\t%s\n", p.PID, p.Parent, p.Threads, p.Name)
	}
	return nil
}

func filetimeValue(t windows.Filetime) uint64 {
	return uint64(t.HighDateTime)<<32 | uint64(t.LowDateTime)
}

func openProcess(pid uint32) (*processReader, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION|windows.PROCESS_VM_READ|windows.SYNCHRONIZE, false, pid)
	if err != nil {
		return nil, fmt.Errorf("open PID %d: %w (run at the same privilege level as the target)", pid, err)
	}
	r := &processReader{handle: h}
	var creation, exit, kernel, user windows.Filetime
	if err = windows.GetProcessTimes(h, &creation, &exit, &kernel, &user); err != nil {
		windows.CloseHandle(h)
		return nil, err
	}
	r.creation = filetimeValue(creation)
	return r, nil
}

func (r *processReader) alive() error {
	state, err := windows.WaitForSingleObject(r.handle, 0)
	if err != nil {
		return err
	}
	if state != uint32(windows.WAIT_TIMEOUT) {
		return errors.New("process exited; restart collection with fresh PIDs")
	}
	return nil
}

func (r *processReader) read() ([]string, error) {
	if err := r.alive(); err != nil {
		return nil, err
	}
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(r.handle, &creation, &exit, &kernel, &user); err != nil {
		return nil, err
	}
	if filetimeValue(creation) != r.creation {
		return nil, errors.New("process identity changed")
	}
	mem := memoryCounters{Size: uint32(unsafe.Sizeof(memoryCounters{}))}
	if ok, _, err := getMemory.Call(uintptr(r.handle), uintptr(unsafe.Pointer(&mem)), uintptr(mem.Size)); ok == 0 {
		return nil, fmt.Errorf("GetProcessMemoryInfo: %w", err)
	}
	var counters windows.IO_COUNTERS
	if ok, _, err := getIO.Call(uintptr(r.handle), uintptr(unsafe.Pointer(&counters))); ok == 0 {
		return nil, fmt.Errorf("GetProcessIoCounters: %w", err)
	}
	var handles uint32
	if ok, _, err := getHandles.Call(uintptr(r.handle), uintptr(unsafe.Pointer(&handles))); ok == 0 {
		return nil, fmt.Errorf("GetProcessHandleCount: %w", err)
	}
	if err := r.alive(); err != nil {
		return nil, err
	}
	values := []string{strconv.FormatFloat(float64(filetimeValue(kernel)+filetimeValue(user))/1e7, 'f', 7, 64)}
	for _, n := range []uint64{uint64(mem.WorkingSet), uint64(mem.PrivateUsage), counters.ReadTransferCount, counters.WriteTransferCount, counters.OtherTransferCount, uint64(handles)} {
		values = append(values, strconv.FormatUint(n, 10))
	}
	return values, nil
}

func waitContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func collect(ctx context.Context, o collectOptions, out io.Writer) (resultErr error) {
	if err := os.Mkdir(o.Output, 0o700); err != nil {
		return err
	}
	v := windows.RtlGetVersion()
	meta := captureMeta{Schema: 1, Status: "collecting", Scenario: o.Scenario, OS: fmt.Sprintf("Windows %d.%d build %d", v.MajorVersion, v.MinorVersion, v.BuildNumber), Arch: runtime.GOARCH, LogicalCPUs: runtime.NumCPU(), DurationSeconds: o.Duration.Seconds(), IntervalSeconds: o.Interval.Seconds(), Children: o.Children}
	writeMeta := func() error {
		data, err := json.MarshalIndent(meta, "", "  ")
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(o.Output, "capture.json"), append(data, '\n'), 0o600)
	}
	if err := writeMeta(); err != nil {
		return err
	}
	defer func() {
		meta.Status = "complete"
		if resultErr != nil {
			meta.Status = "failed"
			meta.Error = resultErr.Error()
		}
		resultErr = errors.Join(resultErr, writeMeta())
	}()
	fmt.Fprintf(out, "Warmup %s. Set both apps to the intended workload now. No windows will be activated.\n", o.Warmup)
	if err := waitContext(ctx, o.Warmup); err != nil {
		return err
	}
	ps, err := processSnapshot()
	if err != nil {
		return err
	}
	roster, err := resolveGroups(ps, o.Groups, o.Children)
	if err != nil {
		return err
	}
	readers := map[uint32]*processReader{}
	defer func() {
		for _, r := range readers {
			windows.CloseHandle(r.handle)
		}
	}()
	for i, p := range roster {
		if p.PID == uint32(os.Getpid()) {
			return errors.New("collector cannot be included in measured groups")
		}
		r, err := openProcess(p.PID)
		if err != nil {
			return err
		}
		readers[p.PID] = r
		roster[i].Creation = strconv.FormatUint(r.creation, 10)
		fmt.Fprintf(out, "%s: %d %s\n", p.Group, p.PID, p.Name)
	}
	for _, p := range roster {
		if parent := readers[p.Parent]; o.Children && parent != nil && readers[p.PID].creation < parent.creation {
			return fmt.Errorf("PID %d predates its apparent parent: ambiguous reused parent PID; select explicit PIDs with --children=false", p.PID)
		}
	}
	meta.Processes = roster
	f, err := os.OpenFile(filepath.Join(o.Output, "samples.csv"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, f.Close()) }()
	w := csv.NewWriter(f)
	if err = w.Write(windowsHeader); err != nil {
		return err
	}
	w.Flush()
	if err = w.Error(); err != nil {
		return err
	}
	start := time.Now()
	meta.Started = start.UTC().Format(time.RFC3339Nano)
	if err = writeMeta(); err != nil {
		return err
	}
	for {
		if err = ctx.Err(); err != nil {
			return err
		}
		batchStart := time.Now()
		ps, err = processSnapshot()
		if err != nil {
			return err
		}
		current, err := resolveGroups(ps, o.Groups, o.Children)
		if err != nil {
			return err
		}
		if len(current) != len(roster) {
			return errors.New("process tree changed during collection; wait for helpers to stabilize and retry")
		}
		for i, p := range current {
			if p.PID != roster[i].PID || p.Group != roster[i].Group {
				return errors.New("process group membership changed; retry")
			}
		}
		elapsed := time.Since(start)
		rows := make([][]string, 0, len(roster))
		for i, p := range roster {
			values, err := readers[p.PID].read()
			if err != nil {
				return fmt.Errorf("PID %d: %w", p.PID, err)
			}
			row := []string{strconv.FormatFloat(elapsed.Seconds(), 'f', 9, 64), p.Group, strconv.FormatUint(uint64(p.PID), 10), p.Creation}
			row = append(row, values...)
			row = append(row, strconv.FormatUint(uint64(current[i].Threads), 10))
			rows = append(rows, row)
		}
		// Commit only complete batches. Read failures never become zero-valued samples.
		for _, row := range rows {
			if err = w.Write(row); err != nil {
				return err
			}
		}
		w.Flush()
		if err = w.Error(); err != nil {
			return err
		}
		meta.Samples++
		if span := time.Since(batchStart).Seconds(); span > meta.MaxSampleSpanSeconds {
			meta.MaxSampleSpanSeconds = span
		}
		if elapsed >= o.Duration {
			break
		}
		next := (time.Since(start)/o.Interval + 1) * o.Interval
		if next > o.Duration {
			next = o.Duration
		}
		if err = waitContext(ctx, time.Until(start.Add(next))); err != nil {
			return err
		}
	}
	if err = f.Sync(); err != nil {
		return err
	}
	fmt.Fprintf(out, "Complete: %d samples/process. CSV: %s\n", meta.Samples, filepath.Join(o.Output, "samples.csv"))
	return nil
}
