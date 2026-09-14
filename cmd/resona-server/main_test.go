package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/tsukiyoz/resona/internal/noiseudp"
)

func TestIdentityDoesNotOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "noise.key")
	key, err := noiseudp.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := writeNew(path, key); err != nil {
		t.Fatal(err)
	}
	if err := writeNew(path, []byte("replacement")); err == nil {
		t.Fatal("overwrote existing identity")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(key, after) {
		t.Fatal("existing key changed")
	}
}
