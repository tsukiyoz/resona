// Package iconcache caches validated PNG thumbnails for protocol adapters.
package iconcache

import (
	"bytes"
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	maxPNGBytes  = 256 << 10
	maxDimension = 256
	dataPrefix   = "data:image/png;base64,"
	defaultTTL   = 7 * 24 * time.Hour
)

// Key separates servers and the local identity's permission boundary.
// ServerUID is optional when the server does not advertise its identity.
type Key struct {
	Protocol, ServerUID, Address, Endpoint, IdentityUID, IconID, TransformVersion string
}

func (k Key) hash() string {
	h := sha256.New()
	for _, field := range []string{k.Protocol, k.ServerUID, k.Address, k.Endpoint, k.IdentityUID, k.IconID, k.TransformVersion} {
		_ = binary.Write(h, binary.BigEndian, uint64(len(field)))
		_, _ = h.Write([]byte(field))
	}
	return hex.EncodeToString(h.Sum(nil))
}

type entry struct {
	key, data string
	created   time.Time
}

type flight struct {
	done chan struct{}
	data string
	err  error
}

// Cache is shared across connections. mu only protects memory bookkeeping;
// filesystem and network operations never hold it.
type Cache struct {
	mu                                                 sync.Mutex
	items                                              map[string]*list.Element
	lru                                                list.List
	bytes                                              int
	flights                                            map[string]*flight
	diskMu                                             sync.Mutex
	dir                                                string
	now                                                func() time.Time
	ttl                                                time.Duration
	memoryBytes, memoryEntries, diskBytes, diskEntries int
}

func New(dir string) *Cache {
	return &Cache{
		items: make(map[string]*list.Element), flights: make(map[string]*flight), dir: dir,
		now: time.Now, ttl: defaultTTL, memoryBytes: 8 << 20, memoryEntries: 128,
		diskBytes: 64 << 20, diskEntries: 512,
	}
}

// NewDefault keeps memory caching available when the OS cache path fails.
func NewDefault() *Cache {
	dir, err := os.UserCacheDir()
	if err != nil {
		return New("")
	}
	return New(filepath.Join(dir, "resona", "icons"))
}

// Get tries memory, disk, then fetch. Concurrent callers share an attempt;
// canceled callers leave promptly. Failed attempts are never cached.
func (c *Cache) Get(ctx context.Context, key Key, fetch func(context.Context) (string, error)) (string, error) {
	return c.get(ctx, key.hash(), fetch)
}

// Reference loads a validated thumbnail and returns its opaque, scoped handle.
func (c *Cache) Reference(ctx context.Context, key Key, fetch func(context.Context) (string, error)) (string, error) {
	if _, err := c.Get(ctx, key, fetch); err != nil {
		return "", err
	}
	return key.hash(), nil
}

// Read never downloads. The session owner must authorize the reference first.
func (c *Cache) Read(ctx context.Context, ref string) (string, error) {
	decoded, err := hex.DecodeString(ref)
	if err != nil || len(decoded) != sha256.Size || strings.ToLower(ref) != ref {
		return "", errors.New("invalid icon reference")
	}
	return c.get(ctx, ref, func(context.Context) (string, error) {
		return "", errors.New("icon resource is no longer cached")
	})
}

func (c *Cache) get(ctx context.Context, hash string, fetch func(context.Context) (string, error)) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	c.mu.Lock()
	if item := c.items[hash]; item != nil {
		e := item.Value.(entry)
		if c.fresh(e.created) {
			c.lru.MoveToFront(item)
			c.mu.Unlock()
			return e.data, nil
		}
		c.remove(item)
	}
	if pending := c.flights[hash]; pending != nil {
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-pending.done:
			return pending.data, pending.err
		}
	}
	pending := &flight{done: make(chan struct{})}
	c.flights[hash] = pending
	c.mu.Unlock()
	data, created, err := c.load(ctx, hash, fetch)
	if ctx.Err() != nil {
		data, err = "", ctx.Err()
	}
	c.mu.Lock()
	if err == nil {
		c.items[hash] = c.lru.PushFront(entry{key: hash, data: data, created: created})
		c.bytes += len(data)
		for c.bytes > c.memoryBytes || len(c.items) > c.memoryEntries {
			c.remove(c.lru.Back())
		}
	}
	pending.data, pending.err = data, err
	delete(c.flights, hash)
	close(pending.done)
	c.mu.Unlock()
	return data, err
}

func (c *Cache) remove(item *list.Element) {
	e := item.Value.(entry)
	c.bytes -= len(e.data)
	delete(c.items, e.key)
	c.lru.Remove(item)
}

func (c *Cache) fresh(created time.Time) bool {
	age := c.now().Sub(created)
	return age >= 0 && age < c.ttl
}

func (c *Cache) load(ctx context.Context, hash string, fetch func(context.Context) (string, error)) (string, time.Time, error) {
	if data, created, ok := c.readDisk(ctx, hash); ok {
		return data, created, nil
	}
	if err := ctx.Err(); err != nil {
		return "", time.Time{}, err
	}
	data, err := fetch(ctx)
	if err != nil {
		return "", time.Time{}, err
	}
	if err := ctx.Err(); err != nil {
		return "", time.Time{}, err
	}
	pngData, err := decodeDataURL(data)
	if err != nil {
		return "", time.Time{}, err
	}
	created := c.now()
	c.writeDisk(ctx, hash, pngData, created)
	return data, created, nil
}

func decodeDataURL(data string) ([]byte, error) {
	if !strings.HasPrefix(data, dataPrefix) || len(data) > len(dataPrefix)+base64.StdEncoding.EncodedLen(maxPNGBytes) {
		return nil, errors.New("invalid cached icon data URL")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(data, dataPrefix))
	if err != nil {
		return nil, err
	}
	if err := validatePNG(raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func validatePNG(raw []byte) error {
	if len(raw) == 0 || len(raw) > maxPNGBytes {
		return errors.New("invalid cached PNG size")
	}
	config, err := png.DecodeConfig(bytes.NewReader(raw))
	if err != nil || config.Width <= 0 || config.Height <= 0 || config.Width > maxDimension || config.Height > maxDimension {
		return errors.New("invalid cached PNG dimensions")
	}
	if _, err := png.Decode(bytes.NewReader(raw)); err != nil {
		return err
	}
	return nil
}
