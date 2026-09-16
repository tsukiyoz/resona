// Package diagnostics records bounded, best-effort client events in memory.
package diagnostics

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tsukiyoz/resona/internal/version"
)

const Capacity = 512
const Window = 15 * time.Minute

type field struct {
	key             [32]byte
	text            [96]byte
	keyLen, textLen uint8
	kind            slog.Kind
	number          uint64
}
type event struct {
	at         time.Time
	message    [64]byte
	messageLen uint8
	level      slog.Level
	fields     [10]field
	count      uint8
}
type buffer struct {
	mu          sync.Mutex
	events      [Capacity]event
	next, count int
	overwritten uint64
	dropped     atomic.Uint64
	exporting   atomic.Bool
}
type Recorder struct {
	buffer  *buffer
	base    event
	grouped bool
}

func New() *Recorder                                               { return &Recorder{buffer: &buffer{}} }
func (*Recorder) Enabled(_ context.Context, level slog.Level) bool { return level >= slog.LevelInfo }

// Only explicitly reviewed events and primitive fields can enter an export.
func allowedMessage(s string) bool {
	switch s {
	case "keychain operation started", "keychain operation finished":
		return true
	case "client started", "client stopped", "core stopped", "connection requested", "connection failed", "connection established", "connection lost", "connection cancelled by user", "reconnect scheduled", "reconnect failed", "reconnect succeeded", "native transport closed", "voice devices ready", "voice configuration failed", "voice routing interval", "voice counters", "voice peer interval":
		return true
	}
	return false
}
func allowedKey(s string) bool {
	switch s {
	case "operation", "operation_id", "status", "os_status":
		return true
	case "component", "version", "timeout", "retryable", "attempt", "delay_ms", "elapsed_ms", "channel_restored", "reason", "capture", "deafened", "activation", "local_monitor", "cancelled", "received", "malformed", "stale_member_or_epoch", "no_audio_handler", "delivered", "receive_queue_drops", "sent", "send_errors", "capture_dropped_samples", "final", "sender", "decoded", "decode_errors", "jitter_rejected", "unknown_peer", "muted_frames":
		return true
	}
	return false
}
func (e *event) add(a slog.Attr) {
	if !allowedKey(a.Key) || int(e.count) == len(e.fields) {
		return
	}
	f := field{kind: a.Value.Kind()}
	f.keyLen = uint8(copy(f.key[:], a.Key))
	switch f.kind {
	case slog.KindString:
		f.textLen = uint8(copy(f.text[:], a.Value.String()))
	case slog.KindBool:
		if a.Value.Bool() {
			f.number = 1
		}
	case slog.KindInt64:
		f.number = uint64(a.Value.Int64())
	case slog.KindUint64:
		f.number = a.Value.Uint64()
	case slog.KindDuration:
		f.number = uint64(a.Value.Duration())
	default:
		return // Never call arbitrary LogValuer, Stringer or error methods.
	}
	e.fields[e.count] = f
	e.count++
}
func (r *Recorder) Handle(_ context.Context, record slog.Record) error {
	if !allowedMessage(record.Message) {
		return nil
	}
	e := r.base
	e.at = time.Now()
	e.level = record.Level
	e.messageLen = uint8(copy(e.message[:], record.Message))
	if !r.grouped {
		record.Attrs(func(a slog.Attr) bool { e.add(a); return true })
	}
	b := r.buffer
	if !b.mu.TryLock() {
		b.dropped.Add(1)
		return nil
	}
	defer b.mu.Unlock()
	for b.count > 0 && e.at.Sub(b.events[(b.next-b.count+Capacity)%Capacity].at) > Window {
		b.count--
	}
	if b.count == Capacity {
		b.overwritten++
	} else {
		b.count++
	}
	b.events[b.next] = e
	b.next = (b.next + 1) % Capacity
	return nil
}
func (r *Recorder) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := *r
	if !r.grouped {
		for _, a := range attrs {
			next.base.add(a)
		}
	}
	return &next
}
func (r *Recorder) WithGroup(name string) slog.Handler {
	next := *r
	if name != "" {
		next.grouped = true
	}
	return &next
}

type Event struct {
	Time    time.Time      `json:"time"`
	Level   string         `json:"level"`
	Message string         `json:"message"`
	Fields  map[string]any `json:"fields"`
}
type Snapshot struct {
	Version         string    `json:"version"`
	Platform        string    `json:"platform"`
	ExportedAt      time.Time `json:"exportedAt"`
	WindowSeconds   int       `json:"windowSeconds"`
	Capacity        int       `json:"capacity"`
	Overwritten     uint64    `json:"overwritten"`
	ContentionDrops uint64    `json:"contentionDrops"`
	Events          []Event   `json:"events"`
}

func (r *Recorder) Snapshot() Snapshot {
	// Allocate before the lock, format afterwards. Producers never wait on this copy.
	raw := make([]event, Capacity)
	b := r.buffer
	b.mu.Lock()
	now := time.Now()
	n := 0
	for i := 0; i < b.count; i++ {
		e := b.events[(b.next-b.count+i+Capacity)%Capacity]
		if now.Sub(e.at) <= Window {
			raw[n] = e
			n++
		}
	}
	overwritten := b.overwritten
	b.mu.Unlock()
	s := Snapshot{Version: version.Current().String(), Platform: runtime.GOOS + "/" + runtime.GOARCH, ExportedAt: now, WindowSeconds: int(Window.Seconds()), Capacity: Capacity, Overwritten: overwritten, ContentionDrops: b.dropped.Load(), Events: make([]Event, 0, n)}
	for _, e := range raw[:n] {
		out := Event{Time: e.at, Level: e.level.String(), Message: string(e.message[:e.messageLen]), Fields: make(map[string]any, e.count)}
		for _, f := range e.fields[:e.count] {
			var value any
			switch f.kind {
			case slog.KindString:
				value = string(f.text[:f.textLen])
			case slog.KindBool:
				value = f.number != 0
			case slog.KindInt64, slog.KindDuration:
				value = int64(f.number)
			case slog.KindUint64:
				value = f.number
			}
			out.Fields[string(f.key[:f.keyLen])] = value
		}
		s.Events = append(s.Events, out)
	}
	return s
}

// Export is the only file-writing entry point, called explicitly by the user.
func (r *Recorder) Export(ctx context.Context, directory string) (path string, err error) {
	if !r.buffer.exporting.CompareAndSwap(false, true) {
		return "", errors.New("诊断正在导出")
	}
	defer r.buffer.exporting.Store(false)
	if err = ctx.Err(); err != nil {
		return "", err
	}
	snapshot := r.Snapshot()
	if err = ctx.Err(); err != nil {
		return "", err
	}
	if err = os.MkdirAll(directory, 0700); err != nil {
		return "", errors.New("无法创建诊断导出目录")
	}
	f, err := os.CreateTemp(directory, "resona-diagnostics-"+snapshot.ExportedAt.Format("20060102-150405")+"-*.json")
	if err != nil {
		return "", errors.New("无法创建诊断文件")
	}
	name := f.Name()
	defer func() {
		_ = f.Close()
		if err != nil {
			_ = os.Remove(name)
		}
	}()
	if err = ctx.Err(); err != nil {
		return "", err
	}
	if err = json.NewEncoder(f).Encode(snapshot); err != nil {
		return "", errors.New("无法写入诊断文件")
	}
	if err = f.Close(); err != nil {
		return "", errors.New("无法完成诊断文件")
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	return name, nil
}
func ExportDefault(ctx context.Context) (string, error) {
	r, ok := slog.Default().Handler().(*Recorder)
	if !ok {
		return "", errors.New("内存诊断不可用")
	}
	root, err := os.UserCacheDir()
	if err != nil {
		return "", errors.New("无法定位诊断导出目录")
	}
	return r.Export(ctx, filepath.Join(root, "resona", "diagnostics"))
}
