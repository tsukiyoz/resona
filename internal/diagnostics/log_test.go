package diagnostics

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"
)

func TestCapacityWindowAndIsolation(t *testing.T) {
	r := New()
	l := slog.New(r)
	for i := 0; i < Capacity+3; i++ {
		l.Info("reconnect scheduled", "attempt", i)
	}
	s := r.Snapshot()
	if len(s.Events) != Capacity || s.Overwritten != 3 || s.Events[0].Fields["attempt"] != int64(3) {
		t.Fatal("capacity/order", s.Overwritten, len(s.Events))
	}
	for i := range r.buffer.events {
		r.buffer.events[i].at = time.Now().Add(-Window - time.Second)
	}
	if len(r.Snapshot().Events) != 0 {
		t.Fatal("expired events exported")
	}
	l.Info("connection requested")
	if len(r.Snapshot().Events) != 1 || r.buffer.count != 1 {
		t.Fatal("expired events not evicted")
	}
	if len(s.Events) != Capacity {
		t.Fatal("snapshot mutated")
	}
}

type secret struct{}

type cancelBeforeWrite struct {
	context.Context
	calls int
}

func (c *cancelBeforeWrite) Err() error {
	c.calls++
	if c.calls >= 3 {
		return context.Canceled
	}
	return nil
}

func TestExportRemovesPartialOnCancellation(t *testing.T) {
	dir := t.TempDir()
	r := New()
	_, err := r.Export(&cancelBeforeWrite{Context: context.Background()}, dir)
	if err != context.Canceled {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatal("partial file left behind", entries, err)
	}
	if _, err := r.Export(context.Background(), dir); err != nil {
		t.Fatal("export remains busy", err)
	}
}

func (secret) LogValue() slog.Value { panic("must not evaluate arbitrary value") }

func TestPrivacyBoundsAndContention(t *testing.T) {
	r := New()
	l := slog.New(r).With("component", "core")
	l.Info("secret arbitrary message", "password", "secret")
	l.Info("connection failed", "password", "secret", "error", secret{}, "version", strings.Repeat("v", 10000))
	s := r.Snapshot()
	if len(s.Events) != 1 || len(s.Events[0].Fields) != 2 || len(s.Events[0].Fields["version"].(string)) != 96 {
		t.Fatal("privacy/bounds", s)
	}
	r.buffer.mu.Lock()
	l.Info("connection failed")
	r.buffer.mu.Unlock()
	if r.Snapshot().ContentionDrops != 1 {
		t.Fatal("contention must drop instead of block")
	}
}

func TestExportOnlyExplicitlyAndCancellation(t *testing.T) {
	r := New()
	dir := filepath.Join(t.TempDir(), "exports")
	slog.New(r).Info("connection established")
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("recording created files")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.Export(ctx, dir); err != context.Canceled {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("cancelled export created files")
	}
	path, err := r.Export(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var s Snapshot
	if err := json.Unmarshal(data, &s); err != nil || len(s.Events) != 1 {
		t.Fatal("invalid export", err)
	}
	bad := filepath.Join(dir, "file")
	if err := os.WriteFile(bad, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Export(context.Background(), bad); err == nil {
		t.Fatal("expected directory error")
	}
	if _, err := r.Export(context.Background(), dir); err != nil {
		t.Fatal("failed export stuck busy", err)
	}
}

func TestConcurrentRecordAndSnapshot(t *testing.T) {
	r := New()
	l := slog.New(r)
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 1000 {
				l.Info("voice counters", "received", uint64(123))
			}
		})
	}
	wg.Go(func() {
		for range 8 {
			_ = r.Snapshot()
		}
	})
	wg.Wait()
	if len(r.Snapshot().Events) > Capacity {
		t.Fatal("unbounded")
	}
}

func BenchmarkMemoryEvent(b *testing.B) {
	r := New()
	l := slog.New(r)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l.LogAttrs(context.Background(), slog.LevelInfo, "voice counters", slog.Uint64("received", 123), slog.Uint64("send_errors", 0))
	}
	b.ReportMetric(float64(unsafe.Sizeof(buffer{})), "buffer-bytes")
}
