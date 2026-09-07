//go:build darwin && cgo && integration

package credentials

import (
	"crypto/rand"
	"errors"
	"os"
	"testing"
)

func TestNativeKeychainRoundTrip(t *testing.T) {
	if os.Getenv("RESONA_KEYCHAIN_INTEGRATION") != "1" {
		t.Skip("set RESONA_KEYCHAIN_INTEGRATION=1 to test a new temporary Keychain item")
	}
	store := New()
	key := "integration-" + rand.Text()
	t.Cleanup(func() {
		if err := store.Delete(key); err != nil {
			t.Errorf("temporary Keychain item cleanup failed: %v", err)
		}
	})
	if found, err := store.Has(key); err != nil || found {
		t.Fatalf("unexpected initial temporary item state: %v", err)
	}
	for index, value := range []string{"resona-integration-only", "", "resona-integration-replacement", "密碼\n\"\\\x00", ""} {
		if err := store.Set(key, value); err != nil {
			t.Fatal(err)
		}
		if found, err := store.Has(key); err != nil || !found {
			t.Fatalf("temporary item metadata was not found: %v", err)
		}
		actual, err := store.Get(key)
		if err != nil || actual != value {
			t.Fatalf("temporary item password did not round-trip: iteration=%d expected_bytes=%d actual_bytes=%d error=%v", index, len(value), len(actual), err)
		}
	}
	if err := store.Delete(key); err != nil {
		t.Fatal(err)
	}
	if found, err := store.Has(key); err != nil || found {
		t.Fatalf("temporary item remains after deletion: %v", err)
	}
	if _, err := store.Get(key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted temporary item did not report not found: %v", err)
	}
}
