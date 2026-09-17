package iconcache

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	diskMagic       = "RICON001"
	diskHeaderBytes = 8 + 8 + sha256.Size
)

func diskName(hash string) string { return "icon-" + hash + ".bin" }

// Open a confined directory handle and reject symbolic links for both package
// directories. The caller-supplied parent is the trusted OS cache location.
func (c *Cache) openDisk() (*os.Root, error) {
	if c.dir == "" {
		return nil, errors.New("disk cache disabled")
	}
	parent := filepath.Dir(c.dir)
	base := filepath.Dir(parent)
	if err := os.MkdirAll(base, 0o700); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	parentRoot, err := privateDirectory(root, filepath.Base(parent))
	if err != nil {
		return nil, err
	}
	defer parentRoot.Close()
	return privateDirectory(parentRoot, filepath.Base(c.dir))
}

func privateDirectory(parent *os.Root, name string) (*os.Root, error) {
	if err := parent.Mkdir(name, 0o700); err != nil && !os.IsExist(err) {
		return nil, err
	}
	before, err := parent.Lstat(name)
	if err != nil || !before.IsDir() {
		return nil, errors.New("unsafe icon cache directory")
	}
	root, err := parent.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	dir, err := root.Open(".")
	if err != nil {
		root.Close()
		return nil, err
	}
	defer dir.Close()
	after, err := dir.Stat()
	if err != nil || !os.SameFile(before, after) {
		root.Close()
		return nil, errors.New("icon cache directory changed")
	}
	if err := dir.Chmod(0o700); err != nil {
		root.Close()
		return nil, err
	}
	return root, nil
}

func (c *Cache) readDisk(ctx context.Context, hash string) (string, time.Time, bool) {
	if ctx.Err() != nil {
		return "", time.Time{}, false
	}
	c.diskMu.Lock()
	defer c.diskMu.Unlock()
	root, err := c.openDisk()
	if err != nil {
		return "", time.Time{}, false
	}
	defer root.Close()
	name := diskName(hash)
	before, err := root.Lstat(name)
	if err != nil || !before.Mode().IsRegular() || before.Size() > maxPNGBytes+diskHeaderBytes {
		return "", time.Time{}, false
	}
	file, err := root.Open(name)
	if err != nil {
		return "", time.Time{}, false
	}
	defer file.Close()
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) {
		return "", time.Time{}, false
	}
	data, err := io.ReadAll(io.LimitReader(file, maxPNGBytes+diskHeaderBytes+1))
	if err != nil || len(data) <= diskHeaderBytes || len(data) > maxPNGBytes+diskHeaderBytes || string(data[:8]) != diskMagic {
		return "", time.Time{}, false
	}
	created := time.Unix(0, int64(binary.BigEndian.Uint64(data[8:16])))
	raw := data[diskHeaderBytes:]
	checksum := sha256.Sum256(raw)
	if !c.fresh(created) || !bytes.Equal(checksum[:], data[16:diskHeaderBytes]) || validatePNG(raw) != nil || ctx.Err() != nil {
		return "", time.Time{}, false
	}
	return dataPrefix + base64.StdEncoding.EncodeToString(raw), created, true
}

func (c *Cache) writeDisk(ctx context.Context, hash string, raw []byte, created time.Time) {
	if ctx.Err() != nil {
		return
	}
	c.diskMu.Lock()
	defer c.diskMu.Unlock()
	root, err := c.openDisk()
	if err != nil {
		return
	}
	defer root.Close()
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return
	}
	temp := ".icon-tmp-" + hex.EncodeToString(random)
	file, err := root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return
	}
	defer root.Remove(temp)
	header := make([]byte, diskHeaderBytes)
	copy(header, diskMagic)
	binary.BigEndian.PutUint64(header[8:16], uint64(created.UnixNano()))
	checksum := sha256.Sum256(raw)
	copy(header[16:], checksum[:])
	_, headerErr := file.Write(header)
	_, dataErr := file.Write(raw)
	syncErr := file.Sync()
	closeErr := file.Close()
	if headerErr != nil || dataErr != nil || syncErr != nil || closeErr != nil || ctx.Err() != nil {
		return
	}
	if err := root.Rename(temp, diskName(hash)); err != nil {
		return
	}
	c.prune(root)
}

func ownedDiskName(name string) bool {
	if !strings.HasPrefix(name, "icon-") || !strings.HasSuffix(name, ".bin") || len(name) != 73 {
		return false
	}
	hash := strings.TrimSuffix(strings.TrimPrefix(name, "icon-"), ".bin")
	_, err := hex.DecodeString(hash)
	return err == nil
}

// Disk eviction is oldest-written first, never sliding TTL. Atomic publication
// and confined removal are safe across cache instances; concurrent pruning can
// discard an extra entry, which merely causes a future network miss.
func (c *Cache) prune(root *os.Root) {
	dir, err := root.Open(".")
	if err != nil {
		return
	}
	defer dir.Close()
	type candidate struct {
		name    string
		size    int64
		written time.Time
	}
	var entries []candidate
	var size int64
	for {
		batch, err := dir.ReadDir(64)
		for _, item := range batch {
			if !ownedDiskName(item.Name()) {
				continue
			}
			info, err := root.Lstat(item.Name())
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			entries = append(entries, candidate{item.Name(), info.Size(), info.ModTime()})
			size += info.Size()
		}
		if err != nil {
			break
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].written.Equal(entries[j].written) {
			return entries[i].name < entries[j].name
		}
		return entries[i].written.Before(entries[j].written)
	})
	remaining := len(entries)
	for _, item := range entries {
		if size <= int64(c.diskBytes) && remaining <= c.diskEntries {
			break
		}
		if err := root.Remove(item.name); err == nil || os.IsNotExist(err) {
			size -= item.size
			remaining--
		}
	}
}
