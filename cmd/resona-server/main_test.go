package main

import (
	"bytes"
	"crypto/tls"
	"os"
	"path/filepath"
	"testing"
)

func TestCertificateDoesNotOverwrite(t *testing.T) {
	dir := t.TempDir()
	cert, key := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err := createCertificate(cert, key); err != nil {
		t.Fatal(err)
	}
	if _, err := tls.LoadX509KeyPair(cert, key); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := createCertificate(cert, key); err == nil {
		t.Fatal("overwrote existing identity")
	}
	after, err := os.ReadFile(key)
	if err != nil || !bytes.Equal(original, after) {
		t.Fatal("existing key changed")
	}
	newKey := filepath.Join(dir, "new-key.pem")
	if err := createCertificate(cert, newKey); err == nil {
		t.Fatal("overwrote existing certificate")
	}
	if _, err := os.Stat(newKey); !os.IsNotExist(err) {
		t.Fatal("left unmatched new key")
	}
}
