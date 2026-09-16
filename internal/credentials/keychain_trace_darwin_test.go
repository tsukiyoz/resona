//go:build darwin && cgo

package credentials

import (
	"errors"
	"log/slog"
	"testing"

	keychain "github.com/keybase/go-keychain"
	"github.com/tsukiyoz/resona/internal/diagnostics"
)

func TestKeychainTraceRecordsBoundariesWithoutErrorContents(t *testing.T) {
	recorder := diagnostics.New()
	previous := slog.Default()
	slog.SetDefault(slog.New(recorder))
	t.Cleanup(func() { slog.SetDefault(previous) })
	for _, err := range []error{nil, keychain.ErrorAuthFailed, errors.New("secret error detail")} {
		finish := traceKeychainOperation("read")
		finish(err)
	}
	events := recorder.Snapshot().Events
	if len(events) != 6 {
		t.Fatalf("expected three start/end pairs, got %d events", len(events))
	}
	for i := 0; i < len(events); i += 2 {
		start, end := events[i], events[i+1]
		if start.Message != "keychain operation started" || end.Message != "keychain operation finished" ||
			start.Fields["operation_id"] != end.Fields["operation_id"] {
			t.Fatal("operation boundaries cannot be correlated")
		}
		if len(start.Fields) != 2 || len(end.Fields) != 5 || end.Fields["operation"] != "read" {
			t.Fatal("unexpected diagnostic fields")
		}
	}
	if events[1].Fields["status"] != "ok" || events[3].Fields["status"] != "failed" ||
		events[3].Fields["os_status"] != int64(keychain.ErrorAuthFailed) || events[5].Fields["status"] != "failed" {
		t.Fatal("native outcome was not recorded")
	}
}
