// Package nativeidentity owns native client identity keys and atomic private files.
package nativeidentity

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

func UID(public []byte) string {
	sum := sha256.Sum256(public)
	return hex.EncodeToString(sum[:])
}

func LoadOrCreate(path string) (ed25519.PrivateKey, error) {
	data, err := ReadPrivate(path, ed25519.SeedSize)
	if errors.Is(err, os.ErrNotExist) {
		seed := make([]byte, ed25519.SeedSize)
		if _, err = rand.Read(seed); err != nil {
			return nil, err
		}
		if err = WriteNew(path, seed); err != nil && !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		data, err = ReadPrivate(path, ed25519.SeedSize)
	}
	if err != nil {
		return nil, err
	}
	if len(data) != ed25519.SeedSize {
		return nil, errors.New("invalid native identity; original preserved")
	}
	return ed25519.NewKeyFromSeed(data), nil
}

func ReadPrivate(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("invalid private file; original preserved")
	}
	if err = os.Chmod(path, 0600); err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if int64(len(data)) > limit {
		return nil, errors.New("private file too large")
	}
	return data, err
}

// Publish only a complete file and never replace an identity won by another process.
func WriteNew(path string, data []byte) error {
	return writePrivate(path, data, false)
}

// ReplacePrivate publishes a complete private file. Callers serialize writers.
func ReplacePrivate(path string, data []byte) error {
	return writePrivate(path, data, true)
}

func writePrivate(path string, data []byte, replace bool) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".resona-private-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if replace {
		err = os.Rename(f.Name(), path)
	} else {
		err = os.Link(f.Name(), path)
	}
	if err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		d, err := os.Open(dir)
		if err != nil {
			return err
		}
		defer d.Close()
		return d.Sync()
	}
	return nil
}
