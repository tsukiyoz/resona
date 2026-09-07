//go:build !darwin || !cgo

package credentials

import (
	"errors"
	"testing"
)

func TestUnsupportedPlatformReturnsUnavailable(t *testing.T) {
	store := New()
	if found, err := store.Has("test-key"); found || err != nil {
		t.Fatal("unsupported storage should have no remembered password")
	}
	if value, err := store.Get("test-key"); value != "" || !errors.Is(err, ErrUnavailable) {
		t.Fatal("Get did not report unavailable secure storage")
	}
	if err := store.Set("test-key", "test-only"); !errors.Is(err, ErrUnavailable) {
		t.Fatal("Set did not report unavailable secure storage")
	}
	if err := store.Delete("test-key"); err != nil {
		t.Fatal("discarding an absent password must allow an ordinary connection")
	}
}
