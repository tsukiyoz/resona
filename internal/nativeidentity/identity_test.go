package nativeidentity

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestIdentityConcurrentCreationAndReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity.key")
	var wg sync.WaitGroup
	keys := make(chan []byte, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			key, err := LoadOrCreate(path)
			if err != nil {
				t.Error(err)
				return
			}
			keys <- key
		}()
	}
	wg.Wait()
	close(keys)
	key, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	for other := range keys {
		if !bytes.Equal(key, other) {
			t.Fatal("identity changed")
		}
	}
}

func TestInvalidIdentityPreserved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity.key")
	if err := os.WriteFile(path, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreate(path); err == nil {
		t.Fatal("corrupt identity replaced")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "broken" {
		t.Fatal("original changed")
	}
}
