package handshake_test

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"testing"

	"github.com/honeybbq/teamspeak-go/handshake"
)

// Real-world TeamSpeak anonymous license captured from a live server handshake.
const testLicenseBase64 = "AQBgjAAqtcBUrw5futTtkl3+EM3OW4Lal6OTPlwuv4xV/gIRFlEAG0Nl" +
	"AAcAAAAgQW5vbnltb3VzAACWSZf+Mjl5RT5mu4rvf8nhAZp9TjXO10XfGHQ9HQPtHiAYiqjtGItRrQ=="

func decodeTestLicense(t *testing.T) []byte {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString(testLicenseBase64)
	if err != nil {
		t.Fatalf("base64 decode failed: %v", err)
	}

	return data
}

func TestParseLicensesValid(t *testing.T) {
	data := decodeTestLicense(t)
	chain, err := handshake.ParseLicenses(data)
	if err != nil {
		t.Fatalf("ParseLicenses failed: %v", err)
	}
	if len(chain.Blocks) != 2 {
		t.Errorf("expected 2 blocks, got %d", len(chain.Blocks))
	}
}

func TestParseLicensesEmptyInput(t *testing.T) {
	_, err := handshake.ParseLicenses([]byte{})
	if err == nil {
		t.Error("expected error for empty input")
	}
}

func TestParseLicensesWrongVersion(t *testing.T) {
	// Version byte at index 0 must be 1
	_, err := handshake.ParseLicenses([]byte{0x02, 0x00})
	if err == nil {
		t.Error("expected error for unsupported version")
	}
}

func TestParseLicensesTooShortBlock(t *testing.T) {
	// Version OK but block data too short (< 42 bytes)
	data := make([]byte, 10)
	data[0] = 0x01 // valid version
	// remaining 9 bytes are not enough for a license block (needs 42)
	_, err := handshake.ParseLicenses(data)
	if err == nil {
		t.Error("expected error for truncated block")
	}
}

func TestDeriveKeyLength(t *testing.T) {
	data := decodeTestLicense(t)
	chain, err := handshake.ParseLicenses(data)
	if err != nil {
		t.Fatal(err)
	}
	key, err := chain.DeriveKey()
	if err != nil {
		t.Fatalf("DeriveKey failed: %v", err)
	}
	if len(key) != 32 {
		t.Errorf("expected 32-byte key, got %d bytes", len(key))
	}
}

func TestDeriveKeyDeterministic(t *testing.T) {
	data := decodeTestLicense(t)
	chain, err := handshake.ParseLicenses(data)
	if err != nil {
		t.Fatal(err)
	}
	key1, err := chain.DeriveKey()
	if err != nil {
		t.Fatal(err)
	}
	key2, err := chain.DeriveKey()
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(key1) != hex.EncodeToString(key2) {
		t.Error("DeriveKey is not deterministic")
	}
}

func TestDeriveKeyNonZero(t *testing.T) {
	data := decodeTestLicense(t)
	chain, err := handshake.ParseLicenses(data)
	if err != nil {
		t.Fatal(err)
	}
	key, err := chain.DeriveKey()
	if err != nil {
		t.Fatal(err)
	}
	allZero := true
	for _, b := range key {
		if b != 0 {
			allZero = false

			break
		}
	}
	if allZero {
		t.Error("derived key should not be all zeros")
	}
}

func TestParseLicensesExpectedKeyKnownValue(t *testing.T) {
	// Known expected key derived from this specific anonymous license.
	const expectedKeyHex = "82a168e11f9f3e3496fbf8479cd3e17d9b0945e224a71fb371af619a256b8446"
	data := decodeTestLicense(t)
	chain, err := handshake.ParseLicenses(data)
	if err != nil {
		t.Fatal(err)
	}
	key, err := chain.DeriveKey()
	if err != nil {
		t.Fatal(err)
	}
	if got := hex.EncodeToString(key); got != expectedKeyHex {
		t.Errorf("DeriveKey = %s, want %s", got, expectedKeyHex)
	}
}

// TS5/TS6 server license block (type 8) tests

// buildTs5LicenseBlob constructs a synthetic version-1 license containing a
// single Ts5Server block (type 8) with the given properties.
func buildTs5LicenseBlob(props [][]byte) []byte {
	// Block layout:
	//   [0]     key kind = 0
	//   [1:33]  32-byte Ed25519 public key (identity point)
	//   [33]    block type = 8
	//   [34:38] not valid before (BE uint32)
	//   [38:42] not valid after  (BE uint32)
	//   [42]    server license type
	//   [43]    property count
	//   [44+]   length-prefixed properties
	const headerSize = 44
	totalPropsSize := 0
	for _, p := range props {
		totalPropsSize += 1 + len(p)
	}
	block := make([]byte, headerSize, headerSize+totalPropsSize)
	block[0] = 0x00
	block[1] = 0x01 // Ed25519 identity point (0,1)
	block[33] = 0x08
	binary.BigEndian.PutUint32(block[34:38], 0x00000000)
	binary.BigEndian.PutUint32(block[38:42], 0x7FFFFFFF)
	block[42] = 7
	block[43] = byte(len(props))
	for _, p := range props {
		block = append(block, byte(len(p)))
		block = append(block, p...)
	}

	return append([]byte{0x01}, block...) // version prefix
}

func TestParseTs5ServerBlock(t *testing.T) {
	data := buildTs5LicenseBlob([][]byte{
		[]byte("issuer.example.com"),
		{0xDE, 0xAD},
	})
	chain, err := handshake.ParseLicenses(data)
	if err != nil {
		t.Fatalf("ParseLicenses failed: %v", err)
	}
	if len(chain.Blocks) != 1 {
		t.Errorf("expected 1 block, got %d", len(chain.Blocks))
	}
	key, err := chain.DeriveKey()
	if err != nil {
		t.Fatalf("DeriveKey failed: %v", err)
	}
	if len(key) != 32 {
		t.Errorf("expected 32-byte key, got %d", len(key))
	}
}

func TestParseTs5ServerBlockZeroProperties(t *testing.T) {
	data := buildTs5LicenseBlob(nil)
	chain, err := handshake.ParseLicenses(data)
	if err != nil {
		t.Fatalf("ParseLicenses failed: %v", err)
	}
	if len(chain.Blocks) != 1 {
		t.Errorf("expected 1 block, got %d", len(chain.Blocks))
	}
}

func TestParseTs5ServerBlockTruncatedPropertyData(t *testing.T) {
	data := buildTs5LicenseBlob([][]byte{{0x01, 0x02, 0x03}})
	// Chop off last byte so the property data is incomplete.
	data = data[:len(data)-1]
	_, err := handshake.ParseLicenses(data)
	if err == nil {
		t.Error("expected error for truncated property data")
	}
}

func TestParseTs5ServerBlockTruncatedPropertyLength(t *testing.T) {
	// Claim 2 properties but only provide 1.
	data := buildTs5LicenseBlob([][]byte{{0xAA}})
	data[1+43] = 2 // override property count to 2
	_, err := handshake.ParseLicenses(data)
	if err == nil {
		t.Error("expected error when property count exceeds available data")
	}
}
