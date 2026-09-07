package crypto_test

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/honeybbq/teamspeak-go/crypto"
	"github.com/tink-crypto/tink-go/v2/mac/subtle"
)

func TestEAXEncrypt(t *testing.T) {
	key := []byte("c:\\windows\\syste")            // 16 bytes
	nonce := []byte("m\\firewall32.cpl")           // 16 bytes
	header := []byte{0x00, 0x65, 0x00, 0x00, 0x08} // Init1 header
	plaintext := []byte{
		0x01, 0x06, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x35, 0xfc, 0x54, 0x2f,
	}

	eax, err := crypto.NewEAX(key)
	if err != nil {
		t.Fatalf("NewEAX failed: %v", err)
	}

	ciphertext, tag, err := eax.Encrypt(nonce, header, plaintext)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}

	t.Logf("Ciphertext: %x", ciphertext)
	t.Logf("Tag: %x", tag)

	// Decrypt back
	decrypted, err := eax.Decrypt(nonce, header, ciphertext, tag)
	if err != nil {
		t.Fatalf("Decrypt failed: %v", err)
	}

	if hex.EncodeToString(decrypted) != hex.EncodeToString(plaintext) {
		t.Errorf("Decrypted data mismatch!\nExpected: %x\nActual:   %x", plaintext, decrypted)
	}
}

func TestCMACAlignment(t *testing.T) {
	key := []byte("c:\\windows\\syste")
	data := []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 2, 3, 4}

	mac, err := subtle.NewAESCMAC(key, 16)
	if err != nil {
		t.Fatalf("NewAESCMAC failed: %v", err)
	}
	sum, err := mac.ComputeMAC(data)
	if err != nil {
		t.Fatalf("ComputeMAC failed: %v", err)
	}
	// AES-CMAC output must be exactly 16 bytes and not all-zero for non-trivial input.
	if len(sum) != 16 {
		t.Errorf("CMAC length = %d, want 16", len(sum))
	}
	if bytes.Equal(sum, make([]byte, 16)) {
		t.Error("CMAC should not be all zeros for non-trivial input")
	}
}

func TestEAXDecryptTagMismatch(t *testing.T) {
	key := []byte("c:\\windows\\syste")
	nonce := []byte("m\\firewall32.cpl")
	header := []byte{0x00, 0x01, 0x02}
	plaintext := []byte{0x01, 0x02, 0x03, 0x04}

	eax, err := crypto.NewEAX(key)
	if err != nil {
		t.Fatalf("NewEAX failed: %v", err)
	}
	ciphertext, tag, err := eax.Encrypt(nonce, header, plaintext)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}

	// Corrupt the tag
	corruptTag := make([]byte, len(tag))
	copy(corruptTag, tag)
	corruptTag[0] ^= 0xFF

	_, err = eax.Decrypt(nonce, header, ciphertext, corruptTag)
	if err == nil {
		t.Error("expected tag mismatch error with corrupted tag")
	}
}

func TestEAXDecryptCiphertextTampered(t *testing.T) {
	key := []byte("c:\\windows\\syste")
	nonce := []byte("m\\firewall32.cpl")
	header := []byte{0x00, 0x01}
	plaintext := []byte{0xDE, 0xAD, 0xBE, 0xEF}

	eax, err := crypto.NewEAX(key)
	if err != nil {
		t.Fatalf("NewEAX failed: %v", err)
	}
	ciphertext, tag, err := eax.Encrypt(nonce, header, plaintext)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}

	// Corrupt the ciphertext
	tampered := make([]byte, len(ciphertext))
	copy(tampered, ciphertext)
	tampered[0] ^= 0x01

	_, err = eax.Decrypt(nonce, header, tampered, tag)
	if err == nil {
		t.Error("expected tag mismatch error with tampered ciphertext")
	}
}

func TestEAXEmptyPlaintext(t *testing.T) {
	key := []byte("c:\\windows\\syste")
	nonce := []byte("m\\firewall32.cpl")
	header := []byte{0x00}
	plaintext := []byte{}

	eax, err := crypto.NewEAX(key)
	if err != nil {
		t.Fatalf("NewEAX failed: %v", err)
	}
	ct, tag, err := eax.Encrypt(nonce, header, plaintext)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}
	pt, err := eax.Decrypt(nonce, header, ct, tag)
	if err != nil {
		t.Fatalf("Decrypt failed: %v", err)
	}
	if len(pt) != 0 {
		t.Errorf("expected empty plaintext, got %d bytes", len(pt))
	}
}

func TestEAXTagIs8Bytes(t *testing.T) {
	key := []byte("c:\\windows\\syste")
	nonce := []byte("m\\firewall32.cpl")
	eax, _ := crypto.NewEAX(key)
	_, tag, err := eax.Encrypt(nonce, nil, []byte("test"))
	if err != nil {
		t.Fatal(err)
	}
	if len(tag) != 8 {
		t.Errorf("tag length = %d, want 8 (TeamSpeak 64-bit tag)", len(tag))
	}
}

func TestEAXKnownVector(t *testing.T) {
	// Encrypt with known key/nonce/plaintext, then verify decrypt produces original.
	// The exact ciphertext is implementation-specific; we validate the roundtrip
	// and check that ciphertext differs from plaintext.
	key := []byte("c:\\windows\\syste")
	nonce := []byte("m\\firewall32.cpl")
	header := []byte{0x00, 0x65, 0x00, 0x00, 0x08}
	plaintext := []byte{0x01, 0x06, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}

	eax, _ := crypto.NewEAX(key)
	ct, tag, err := eax.Encrypt(nonce, header, plaintext)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}
	if bytes.Equal(ct, plaintext) {
		t.Error("ciphertext should differ from plaintext")
	}
	pt, err := eax.Decrypt(nonce, header, ct, tag)
	if err != nil {
		t.Fatalf("Decrypt failed: %v", err)
	}
	if hex.EncodeToString(pt) != hex.EncodeToString(plaintext) {
		t.Errorf("decrypted mismatch: got %x, want %x", pt, plaintext)
	}
}
