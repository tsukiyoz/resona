package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tsukiyoz/resona/internal/client"
)

func TestPersistenceRoundTripAndPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resona", "servers.json")
	store := New(path)
	profiles, err := store.Load()
	if err != nil || len(profiles) != 0 {
		t.Fatalf("first load: %v, %v", profiles, err)
	}
	want := []client.ServerProfile{{ID: "one", Name: "Home", Address: "localhost:9987", Nickname: "Alice"}}
	if err := store.Save(want); err != nil {
		t.Fatal(err)
	}
	got, err := New(path).Load()
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip: %+v, %v", got, err)
	}
	for _, entry := range []struct {
		path string
		mode os.FileMode
	}{{path, 0600}, {filepath.Dir(path), 0700}} {
		info, err := os.Stat(entry.path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != entry.mode {
			t.Errorf("%s mode %o, want %o", entry.path, info.Mode().Perm(), entry.mode)
		}
	}
	if err := store.Save([]client.ServerProfile{}); err != nil {
		t.Fatal(err)
	}
	got, err = store.Load()
	if err != nil || len(got) != 0 {
		t.Fatalf("empty round trip: %+v, %v", got, err)
	}
}

func TestInvalidConfigPreventsServiceStartupAndPreservesFile(t *testing.T) {
	for _, data := range []string{
		"{broken", "[] trailing", `[{"id":"one","name":"Home","address":"localhost","nickname":"Alice","password":"secret"}]`,
		`[{"id":"one","name":"Home","address":"localhost","nickname":"Alice"},{"id":"one","name":"Duplicate","address":"localhost","nickname":"Alice"}]`,
	} {
		t.Run(data, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "servers.json")
			if err := os.WriteFile(path, []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := client.New(New(path)); err == nil {
				t.Fatal("invalid config must prevent startup")
			}
			got, err := os.ReadFile(path)
			if err != nil || string(got) != data {
				t.Fatal("invalid config was modified")
			}
		})
	}
}
