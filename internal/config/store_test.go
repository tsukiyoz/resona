package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/tsukiyoz/resona/internal/client"
)

func TestObsoleteBookmarksRemainManageableWithoutChangingFileOnLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "servers.json")
	profiles := []map[string]any{
		{"id": "old", "name": "Old", "address": "old.invalid", "nickname": "Tester", "protocol": "unsupported", "certificateFingerprint": "retired"},
		{"id": "live", "name": "Native", "address": "localhost:9988", "nickname": "Tester", "protocol": "resona-noise", "serverPublicKey": "abababababababababababababababababababababababababababababababab"},
	}
	data, _ := json.Marshal(profiles)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	service, err := client.New(New(path))
	if err != nil {
		t.Fatal(err)
	}
	workspace, _ := service.GetWorkspace()
	if len(workspace.Servers) != 2 || client.ValidateServerTrust(workspace.Servers[0]) == nil || client.ValidateServerTrust(workspace.Servers[1]) != nil {
		t.Fatal("lost or reinterpreted bookmarks")
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(data) {
		t.Fatal("load rewrote user configuration")
	}
	if _, err := service.DeleteServer("old"); err != nil {
		t.Fatal(err)
	}
	loaded, err := New(path).Load()
	if err != nil || len(loaded) != 1 || loaded[0].ID != "live" {
		t.Fatal("obsolete bookmark cannot be deleted")
	}
}

func TestPersistenceRoundTripAndPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resona", "servers.json")
	store := New(path)
	profiles, err := store.Load()
	if err != nil || len(profiles) != 0 {
		t.Fatalf("first load: %v, %v", profiles, err)
	}
	want := []client.ServerProfile{{Protocol: "resona-noise", ServerPublicKey: "abababababababababababababababababababababababababababababababab", ID: "one", Name: "Home", Address: "localhost:9988", Nickname: "Alice"}}
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
		if runtime.GOOS != "windows" && info.Mode().Perm() != entry.mode {
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
