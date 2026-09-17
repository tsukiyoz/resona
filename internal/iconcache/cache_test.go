package iconcache

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func thumbnail(t *testing.T, size int) string {
	t.Helper()
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewNRGBA(image.Rect(0, 0, size, size))); err != nil {
		t.Fatal(err)
	}
	return dataPrefix + base64.StdEncoding.EncodeToString(data.Bytes())
}

func testKey() Key {
	return Key{Protocol: "resona-noise", ServerUID: "server", Address: "example:9987", Endpoint: "127.0.0.1:9987", IdentityUID: "user", IconID: "1000", TransformVersion: "v1"}
}

func testDir(t *testing.T) string { return filepath.Join(t.TempDir(), "resona", "icons") }

func countFiles(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, entry := range entries {
		if ownedDiskName(entry.Name()) {
			count++
		}
	}
	return count
}

func TestMemoryThenDiskAvoidNetwork(t *testing.T) {
	dir, data, key := testDir(t), thumbnail(t, 16), testKey()
	var calls int
	fetch := func(context.Context) (string, error) { calls++; return data, nil }
	cache := New(dir)
	for range 3 {
		if got, err := cache.Get(context.Background(), key, fetch); err != nil || got != data {
			t.Fatalf("Get = %q, %v", got, err)
		}
	}
	if calls != 1 {
		t.Fatalf("memory fetched %d times", calls)
	}
	restarted := New(dir)
	if got, err := restarted.Get(context.Background(), key, fetch); err != nil || got != data {
		t.Fatalf("restart = %q, %v", got, err)
	}
	if calls != 1 {
		t.Fatalf("disk hit fetched %d times", calls)
	}
	for _, path := range []string{filepath.Dir(dir), dir} {
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o700) {
			t.Fatalf("directory permission: %v, %v", info, err)
		}
	}
	info, err := os.Stat(filepath.Join(dir, diskName(key.hash())))
	if err != nil || !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o600) {
		t.Fatalf("file permission: %v, %v", info, err)
	}
}

func TestTTLDoesNotSlideOnMemoryOrDiskHits(t *testing.T) {
	dir, key, data := testDir(t), testKey(), thumbnail(t, 16)
	start := time.Now().Add(-time.Hour)
	current := start
	cache := New(dir)
	cache.now = func() time.Time { return current }
	calls := 0
	fetch := func(context.Context) (string, error) { calls++; return data, nil }
	if _, err := cache.Get(context.Background(), key, fetch); err != nil {
		t.Fatal(err)
	}
	current = start.Add(defaultTTL - time.Second)
	if _, err := cache.Get(context.Background(), key, fetch); err != nil {
		t.Fatal(err)
	}
	restarted := New(dir)
	restarted.now = cache.now
	if _, err := restarted.Get(context.Background(), key, fetch); err != nil {
		t.Fatal(err)
	}
	current = start.Add(defaultTTL)
	if _, err := restarted.Get(context.Background(), key, fetch); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("TTL extended by hit: %d fetches", calls)
	}
}

func TestCorruptDiskRefetches(t *testing.T) {
	for _, kind := range []string{"truncated", "checksum", "oversized", "future"} {
		t.Run(kind, func(t *testing.T) {
			dir, key, data := testDir(t), testKey(), thumbnail(t, 16)
			fetch := func(context.Context) (string, error) { return data, nil }
			c := New(dir)
			if kind == "future" {
				c.now = func() time.Time { return time.Now().Add(time.Hour) }
			}
			if _, err := c.Get(context.Background(), key, fetch); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, diskName(key.hash()))
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "truncated":
				raw = raw[:10]
			case "checksum":
				raw[len(raw)-1] ^= 1
			case "oversized":
				raw = make([]byte, maxPNGBytes+diskHeaderBytes+1)
			}
			if err := os.WriteFile(path, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			calls := 0
			if _, err := New(dir).Get(context.Background(), key, func(context.Context) (string, error) { calls++; return data, nil }); err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatal("corrupt disk entry was reused")
			}
		})
	}
}

func TestCacheKeySeparatesEveryScope(t *testing.T) {
	dir, data, original := testDir(t), thumbnail(t, 16), testKey()
	c := New(dir)
	calls := 0
	fetch := func(context.Context) (string, error) { calls++; return data, nil }
	if _, err := c.Get(context.Background(), original, fetch); err != nil {
		t.Fatal(err)
	}
	for _, modify := range []func(*Key){
		func(k *Key) { k.Protocol = "native" }, func(k *Key) { k.ServerUID = "server-2" },
		func(k *Key) { k.Address = "other:9987" }, func(k *Key) { k.Endpoint = "127.0.0.2:9987" },
		func(k *Key) { k.IdentityUID = "other-user" }, func(k *Key) { k.IconID = "1001" },
		func(k *Key) { k.TransformVersion = "v2" }, func(k *Key) { k.ServerUID = "" },
	} {
		key := original
		modify(&key)
		if _, err := New(dir).Get(context.Background(), key, fetch); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 9 {
		t.Fatalf("scope collision: %d fetches", calls)
	}
}

func TestMemoryLRUAndByteLimits(t *testing.T) {
	data, c := thumbnail(t, 16), New("")
	c.memoryEntries = 2
	calls := 0
	get := func(id string) {
		t.Helper()
		key := testKey()
		key.IconID = id
		if _, err := c.Get(context.Background(), key, func(context.Context) (string, error) { calls++; return data, nil }); err != nil {
			t.Fatal(err)
		}
	}
	get("1")
	get("2")
	get("1")
	get("3")
	get("1")
	if calls != 3 {
		t.Fatal("recently used entry was evicted")
	}
	get("2")
	if calls != 4 || len(c.items) != 2 {
		t.Fatal("LRU did not evict oldest access")
	}
	c.memoryBytes = len(data) - 1
	get("4")
	if c.bytes != 0 || len(c.items) != 0 {
		t.Fatal("memory byte bound exceeded")
	}
}

func TestDiskEvictionBoundsAndLeavesUnownedFiles(t *testing.T) {
	dir, data := testDir(t), thumbnail(t, 16)
	c := New(dir)
	c.diskEntries = 2
	for n := range 4 {
		key := testKey()
		key.IconID = fmt.Sprint(n)
		if _, err := c.Get(context.Background(), key, func(context.Context) (string, error) { return data, nil }); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, diskName(key.hash()))
		stamp := time.Now().Add(time.Duration(n-10) * time.Hour)
		if err := os.Chtimes(path, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	if got := countFiles(t, dir); got != 2 {
		t.Fatalf("disk count = %d", got)
	}
	old := testKey()
	old.IconID = "0"
	if _, err := os.Stat(filepath.Join(dir, diskName(old.hash()))); !os.IsNotExist(err) {
		t.Fatal("oldest disk entry retained")
	}
	unowned := filepath.Join(dir, "keep.txt")
	if err := os.WriteFile(unowned, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	c.diskBytes = 1
	key := testKey()
	if _, err := c.Get(context.Background(), key, func(context.Context) (string, error) { return data, nil }); err != nil {
		t.Fatal(err)
	}
	if countFiles(t, dir) != 0 {
		t.Fatal("disk byte bound exceeded")
	}
	if _, err := os.Stat(unowned); err != nil {
		t.Fatal("removed unrelated file")
	}
}

func TestErrorsCancellationAndInvalidOutputAreNotCached(t *testing.T) {
	for _, scenario := range []string{"network", "cancel", "svg", "large-png", "corrupt-png", "large-data"} {
		t.Run(scenario, func(t *testing.T) {
			dir, key, data := testDir(t), testKey(), thumbnail(t, 16)
			c := New(dir)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			_, err := c.Get(ctx, key, func(context.Context) (string, error) {
				switch scenario {
				case "network":
					return "", errors.New("download failed")
				case "cancel":
					cancel()
					return data, nil
				case "svg":
					return "data:image/svg+xml,<svg/>", nil
				case "large-png":
					return thumbnail(t, 257), nil
				case "corrupt-png":
					return data[:len(data)-8], nil
				default:
					return dataPrefix + strings.Repeat("A", base64.StdEncoding.EncodedLen(maxPNGBytes)+1), nil
				}
			})
			if err == nil {
				t.Fatal("invalid fetch succeeded")
			}
			if len(c.items) != 0 || countFiles(t, dir) != 0 {
				t.Fatal("failed attempt cached")
			}
			if entries, _ := os.ReadDir(dir); len(entries) != 0 {
				t.Fatal("partial file remains")
			}
			calls := 0
			if _, err := c.Get(context.Background(), key, func(context.Context) (string, error) { calls++; return data, nil }); err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatal("failed attempt suppressed retry")
			}
		})
	}
}

func TestUnavailableDiskFallsBackToMemoryAndNetwork(t *testing.T) {
	parent := t.TempDir()
	occupied := filepath.Join(parent, "resona")
	if err := os.WriteFile(occupied, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, data := New(filepath.Join(occupied, "icons")), thumbnail(t, 16)
	calls := 0
	for range 2 {
		if _, err := c.Get(context.Background(), testKey(), func(context.Context) (string, error) { calls++; return data, nil }); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatal("memory unavailable after disk failure")
	}
	if got, _ := os.ReadFile(occupied); string(got) != "keep" {
		t.Fatal("overwrote occupied cache directory")
	}
}

func TestReadonlyCacheDirectoryFallsBack(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows Chmod does not implement Unix directory write permissions")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses filesystem permissions")
	}
	base := t.TempDir()
	if err := os.Chmod(base, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(base, 0o700)
	c, data := New(filepath.Join(base, "resona", "icons")), thumbnail(t, 16)
	if _, err := c.Get(context.Background(), testKey(), func(context.Context) (string, error) { return data, nil }); err != nil {
		t.Fatal(err)
	}
	if len(c.items) != 1 {
		t.Fatal("readonly disk disabled memory caching")
	}
}

func TestSymlinkDirectoriesAndFilesAreNotFollowed(t *testing.T) {
	for _, kind := range []string{"parent", "directory", "file"} {
		t.Run(kind, func(t *testing.T) {
			base, outside := t.TempDir(), t.TempDir()
			dir := filepath.Join(base, "resona", "icons")
			c, key, data := New(dir), testKey(), thumbnail(t, 16)
			if kind == "parent" {
				if err := os.Symlink(outside, filepath.Dir(dir)); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Mkdir(filepath.Dir(dir), 0o700); err != nil {
					t.Fatal(err)
				}
				if kind == "directory" {
					if err := os.Symlink(outside, dir); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := os.Mkdir(dir, 0o700); err != nil {
						t.Fatal(err)
					}
					target := filepath.Join(outside, "keep")
					if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(target, filepath.Join(dir, diskName(key.hash()))); err != nil {
						t.Fatal(err)
					}
				}
			}
			if _, err := c.Get(context.Background(), key, func(context.Context) (string, error) { return data, nil }); err != nil {
				t.Fatal(err)
			}
			entries, err := os.ReadDir(outside)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "file" {
				raw, _ := os.ReadFile(filepath.Join(outside, "keep"))
				if len(entries) != 1 || string(raw) != "keep" {
					t.Fatal("symlink target modified")
				}
			} else if len(entries) != 0 {
				t.Fatal("symlink directory followed")
			}
		})
	}
}

func TestConcurrentCallersAndCanceledWaiter(t *testing.T) {
	c, key, data := New(testDir(t)), testKey(), thumbnail(t, 16)
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	fetch := func(context.Context) (string, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		return data, nil
	}
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got, err := c.Get(context.Background(), key, fetch); err != nil || got != data {
				t.Errorf("concurrent get: %v", err)
			}
		}()
	}
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	canceled := make(chan error, 1)
	go func() { _, err := c.Get(ctx, key, fetch); canceled <- err }()
	cancel()
	if err := <-canceled; !errors.Is(err, context.Canceled) {
		t.Fatalf("waiter: %v", err)
	}
	close(release)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("concurrent duplicate fetches: %d", calls.Load())
	}
}

func TestConcurrentInstancesPublishCompleteFiles(t *testing.T) {
	dir, key, data := testDir(t), testKey(), thumbnail(t, 16)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := New(dir)
			for range 5 {
				if got, err := c.Get(context.Background(), key, func(context.Context) (string, error) { return data, nil }); err != nil || got != data {
					t.Errorf("get: %v", err)
				}
			}
		}()
	}
	wg.Wait()
	if _, err := New(dir).Get(context.Background(), key, func(context.Context) (string, error) { return "", errors.New("disk entry was not reusable") }); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("partial files remain: %v, %v", entries, err)
	}
}
