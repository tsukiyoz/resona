package nativewire

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"testing"
)

func TestIdentityProofBindsHelloAndTransport(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	binding := bytes.Repeat([]byte{1}, 32)
	hello := Hello{Nickname: "tester", Password: "test-only", PublicKey: pub}
	proof, err := identityProof(binding, hello)
	if err != nil {
		t.Fatal(err)
	}
	sig := ed25519.Sign(key, proof)
	for _, mutate := range []func(*Hello, []byte){
		func(h *Hello, b []byte) { h.Nickname = "other" },
		func(h *Hello, b []byte) { h.Password = "other" },
		func(h *Hello, b []byte) { b[0]++ },
		func(h *Hello, b []byte) { h.PublicKey = bytes.Repeat([]byte{3}, 32) },
	} {
		h := hello
		b := append([]byte(nil), binding...)
		mutate(&h, b)
		changed, err := identityProof(b, h)
		if err == nil && ed25519.Verify(pub, changed, sig) {
			t.Fatal("changed proof accepted")
		}
	}
	if !ed25519.Verify(pub, proof, sig) {
		t.Fatal("valid signature rejected")
	}
}
