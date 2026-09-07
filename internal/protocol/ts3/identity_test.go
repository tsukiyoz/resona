package ts3

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestIdentityPersistsAcrossConcurrentLoads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resona", "identity.key")
	var wg sync.WaitGroup
	uids := make(chan string, 4)
	for range 4 {
		wg.Go(func() {
			id, err := loadIdentity(context.Background(), path)
			if err != nil {
				t.Error(err)
				return
			}
			uids <- identityUID(id)
		})
	}
	wg.Wait()
	close(uids)
	var first string
	for uid := range uids {
		if first == "" {
			first = uid
		}
		if uid != first {
			t.Fatal("concurrent loads created different identities")
		}
	}
	if first == "" {
		t.Fatal("no identity loaded")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("identity permissions: %v", info.Mode())
	}
	id, err := loadIdentity(context.Background(), path)
	if err != nil || identityUID(id) != first {
		t.Fatal("identity changed across reload")
	}
}

func TestInvalidIdentityPreserved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity.key")
	if err := os.WriteFile(path, []byte("broken-key"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadIdentity(context.Background(), path); err == nil {
		t.Fatal("invalid identity accepted")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "broken-key" {
		t.Fatal("invalid identity overwritten")
	}
}

func TestCanceledIdentityDoesNotCreateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity.key")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := loadIdentity(ctx, path); err == nil {
		t.Fatal("canceled generation succeeded")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("canceled generation wrote file")
	}
}
