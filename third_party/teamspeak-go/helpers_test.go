package teamspeak

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestSplitCommandRows_SingleLine(t *testing.T) {
	rows := splitCommandRows("clientlist")
	if len(rows) != 1 || rows[0] != "clientlist" {
		t.Errorf("unexpected rows: %v", rows)
	}
}

func TestSplitCommandRows_NoArgsNoPipe(t *testing.T) {
	rows := splitCommandRows("hello key=val")
	if len(rows) != 1 || rows[0] != "hello key=val" {
		t.Errorf("unexpected rows: %v", rows)
	}
}

func TestSplitCommandRows_PipeSplitsToPrefixedRows(t *testing.T) {
	rows := splitCommandRows("notifycliententerview clid=1|clid=2|clid=3")
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d: %v", len(rows), rows)
	}
	if rows[0] != "notifycliententerview clid=1" {
		t.Errorf("row[0] = %q", rows[0])
	}
	if rows[1] != "notifycliententerview clid=2" {
		t.Errorf("row[1] = %q", rows[1])
	}
	if rows[2] != "notifycliententerview clid=3" {
		t.Errorf("row[2] = %q", rows[2])
	}
}

func TestSplitCommandRows_EmptyPartSkipped(t *testing.T) {
	// Leading/trailing | in "rest" produces empty parts, which are skipped.
	rows := splitCommandRows("cmd a=1||b=2")
	// empty part skipped → 2 rows
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d: %v", len(rows), rows)
	}
}

func TestSplitCommandRows_NoNameNoSpace(t *testing.T) {
	// A string without a space → treated as a single command with no args.
	rows := splitCommandRows("justcommand")
	if len(rows) != 1 || rows[0] != "justcommand" {
		t.Errorf("unexpected: %v", rows)
	}
}

func TestIsAutoNicknameMatch_ExactMatch(t *testing.T) {
	if !isAutoNicknameMatch("Bot", "Bot") {
		t.Error("exact match should return true")
	}
}

func TestIsAutoNicknameMatch_NumericSuffix(t *testing.T) {
	if !isAutoNicknameMatch("Bot", "Bot123") {
		t.Error("numeric suffix should match")
	}
	if !isAutoNicknameMatch("Bot", "Bot1") {
		t.Error("single digit suffix should match")
	}
}

func TestIsAutoNicknameMatch_NonNumericSuffix(t *testing.T) {
	if isAutoNicknameMatch("Bot", "BotX") {
		t.Error("non-numeric suffix should not match")
	}
	if isAutoNicknameMatch("Bot", "Bot1a") {
		t.Error("alphanumeric suffix should not match")
	}
}

func TestIsAutoNicknameMatch_NoPrefixMatch(t *testing.T) {
	if isAutoNicknameMatch("Bot", "OtherBot") {
		t.Error("different prefix should not match")
	}
	if isAutoNicknameMatch("Bot", "") {
		t.Error("empty string should not match")
	}
}

func TestCommandTracker_RegisterAndResolve(t *testing.T) {
	tr := newCommandTracker()

	rc, ch := tr.register()
	if rc == 0 {
		t.Error("expected non-zero rc")
	}

	go func() {
		time.Sleep(10 * time.Millisecond)
		tr.resolve(rc, nil)
	}()

	select {
	case result := <-ch:
		if result.Err != nil {
			t.Errorf("unexpected error: %v", result.Err)
		}
	case <-time.After(time.Second):
		t.Error("resolve timed out")
	}
}

func TestCommandTracker_CollectAndResolveWithData(t *testing.T) {
	tr := newCommandTracker()
	rc, ch := tr.register()

	tr.collect(map[string]string{"k": "v"})
	tr.resolve(rc, nil)

	result := <-ch
	if len(result.Data) != 1 || result.Data[0]["k"] != "v" {
		t.Errorf("unexpected data: %v", result.Data)
	}
}

func TestCommandTracker_UnregisterPreventsFire(t *testing.T) {
	tr := newCommandTracker()
	rc, ch := tr.register()
	tr.unregister(rc)
	// Resolving after unregister should be a no-op.
	tr.resolve(rc, nil)

	select {
	case <-ch:
		t.Error("channel should not receive after unregister")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestCommandTracker_Reset(t *testing.T) {
	tr := newCommandTracker()
	_, _ = tr.register()
	_, _ = tr.register()
	tr.reset()

	// After reset, pending map should be empty; new resolve is a no-op.
	tr.resolve(1, nil)
	tr.resolve(2, nil)
}

func TestCommandTracker_RCMonotoneIncreasing(t *testing.T) {
	tr := newCommandTracker()
	rc1, _ := tr.register()
	rc2, _ := tr.register()
	if rc2 <= rc1 {
		t.Errorf("expected rc2 > rc1, got rc1=%d rc2=%d", rc1, rc2)
	}
}

func TestCommandThrottle_InitialTokensAllowImmediate(t *testing.T) {
	th := newCommandThrottle()
	// Should not block with fresh tokens.
	ctx := context.Background()
	done := make(chan struct{})
	go func() {
		_ = th.wait(ctx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Error("wait blocked unexpectedly on fresh throttle")
	}
}

func TestCommandThrottle_ContextCancelUnblocks(t *testing.T) {
	th := newCommandThrottle()
	// Drain all tokens (default 5, max 8).
	ctx := context.Background()
	for range 8 {
		_ = th.wait(ctx)
	}

	// Now tokens are exhausted; cancel should unblock.
	cancelCtx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- th.wait(cancelCtx)
	}()

	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled, got %v", err)
		}
	case <-time.After(time.Second):
		t.Error("wait did not unblock after context cancel")
	}
}

func TestCommandThrottle_Concurrent(t *testing.T) {
	th := newCommandThrottle()
	var count atomic.Int32
	ctx := context.Background()
	start := make(chan struct{})

	const goroutines = 5
	done := make(chan struct{}, goroutines)
	for range goroutines {
		go func() {
			<-start
			_ = th.wait(ctx)
			count.Add(1)
			done <- struct{}{}
		}()
	}
	close(start)

	for range goroutines {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Errorf("throttle wait did not complete (got %d/%d)", count.Load(), goroutines)

			return
		}
	}
	if count.Load() != goroutines {
		t.Errorf("expected %d completions, got %d", goroutines, count.Load())
	}
}

func TestParseUint64Value_Valid(t *testing.T) {
	v, err := parseUint64Value("42")
	if err != nil || v != 42 {
		t.Fatalf("expected 42 with nil error, got value=%d err=%v", v, err)
	}
	v, err = parseUint64Value("0")
	if err != nil || v != 0 {
		t.Fatalf("expected 0 with nil error, got value=%d err=%v", v, err)
	}
}

func TestParseUint64Value_Invalid_ReturnsError(t *testing.T) {
	_, err := parseUint64Value("abc")
	if err == nil {
		t.Error("expected parse error for invalid input")
	}
	_, err = parseUint64Value("")
	if err == nil {
		t.Error("expected parse error for empty input")
	}
}
