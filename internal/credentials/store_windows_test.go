package credentials

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsCredentialPayloadAndErrors(t *testing.T) {
	items := map[string][]byte{}
	s := &Store{api: credentialAPI{
		read: func(key string) ([]byte, error) {
			data, ok := items[key]
			if !ok {
				return nil, windows.ERROR_NOT_FOUND
			}
			return append([]byte(nil), data...), nil
		},
		write:  func(key string, data []byte) error { items[key] = append([]byte(nil), data...); return nil },
		delete: func(key string) error { delete(items, key); return nil },
	}}
	if found, err := s.Has("test"); found || err != nil {
		t.Fatal("missing credential reported present")
	}
	if _, err := s.Get("test"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	for _, value := range []string{"test-only", "", "密码\n\x00", "replacement"} {
		if err := s.Set("test", value); err != nil {
			t.Fatal(err)
		}
		got, err := s.Get("test")
		if err != nil || got != value {
			t.Fatal("payload failed round-trip")
		}
		if found, err := s.Has("test"); !found || err != nil {
			t.Fatal("saved credential missing")
		}
	}
	target, _ := credentialTarget("test")
	for _, bad := range []string{`{}`, `{"version":2,"password":"test"}`, `{"version":1,"password":null}`, `invalid`} {
		items[target] = []byte(bad)
		if _, err := s.Get("test"); !errors.Is(err, errRead) {
			t.Fatal("corrupt credential accepted")
		}
	}
	if err := s.Set("test", strings.Repeat("a", maxCredentialBytes)); !errors.Is(err, errWrite) {
		t.Fatal("oversized blob accepted")
	}
	for _, key := range []string{"", "bad\x00key", strings.Repeat("x", 257)} {
		if err := s.Set(key, "test"); !errors.Is(err, errInvalidKey) {
			t.Fatal("invalid key accepted")
		}
	}
	s.api.read = func(string) ([]byte, error) { return nil, windows.ERROR_ACCESS_DENIED }
	if _, err := s.Has("test"); !errors.Is(err, errRead) {
		t.Fatal("access failure hidden")
	}
	s.api.write = func(string, []byte) error { return windows.ERROR_ACCESS_DENIED }
	if err := s.Set("test", "test"); !errors.Is(err, errWrite) {
		t.Fatal("write failure hidden")
	}
	s.api.delete = func(string) error { return windows.ERROR_ACCESS_DENIED }
	if err := s.Delete("test"); !errors.Is(err, errDelete) {
		t.Fatal("delete failure hidden")
	}
}

func TestNativeWindowsCredentialRoundTrip(t *testing.T) {
	if os.Getenv("RESONA_CREDENTIAL_INTEGRATION") != "1" {
		t.Skip("set RESONA_CREDENTIAL_INTEGRATION=1 for a temporary Windows credential")
	}
	s := New()
	key := "integration-" + rand.Text()
	t.Cleanup(func() {
		if err := s.Delete(key); err != nil {
			t.Errorf("temporary credential cleanup failed: %v", err)
		}
	})
	for _, value := range []string{"test-only", "", "密码\n\x00", "replacement"} {
		if err := s.Set(key, value); err != nil {
			t.Fatal(err)
		}
		if ok, err := s.Has(key); err != nil || !ok {
			t.Fatal("credential not present")
		}
		got, err := s.Get(key)
		if err != nil || got != value {
			t.Fatal("native credential round-trip failed")
		}
	}
	if err := s.Delete(key); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(key); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted credential remained")
	}
}

func TestWindowsCredentialUsesVersionedNonemptyBlob(t *testing.T) {
	s := &Store{api: credentialAPI{write: func(target string, data []byte) error {
		if !strings.HasPrefix(target, serviceName+"/") {
			t.Fatal("credential namespace missing")
		}
		var p passwordPayload
		if err := json.Unmarshal(data, &p); err != nil || p.Password == nil || *p.Password != "" || p.Version != 1 {
			t.Fatal("empty password lost")
		}
		return nil
	}}}
	if err := s.Set("test", ""); err != nil {
		t.Fatal(err)
	}
}
