package transport_test

import (
	"bytes"
	"testing"

	"github.com/honeybbq/teamspeak-go/transport"
)

// buildUncompressedQlz constructs a QuickLZ level-1 frame with no compression.
// flags=0x04: level=1 (bits 3:2=01), 3-byte header (bit 1=0), uncompressed (bit 0=0).
func buildUncompressedQlz(payload []byte) []byte {
	if len(payload) > 252 {
		panic("buildUncompressedQlz: payload too large for single-byte header")
	}
	data := make([]byte, 3+len(payload))
	data[0] = 0x04
	data[1] = byte(3 + len(payload))
	data[2] = byte(len(payload))
	copy(data[3:], payload)

	return data
}

// buildAllLiteralQlz constructs a QuickLZ level-1 compressed frame where all
// data is encoded as literals (no back-references). Control word=0 means all
// 32 control bits select the literal path.
// flags=0x05: level=1, 3-byte header, compressed.
func buildAllLiteralQlz(payload []byte) []byte {
	if len(payload) > 248 {
		panic("buildAllLiteralQlz: payload too large for single-byte header")
	}
	data := make([]byte, 0, 3+4+len(payload))
	data = append(data, 0x05)
	data = append(data, byte(7+len(payload)))
	data = append(data, byte(len(payload)))
	data = append(data, 0x00, 0x00, 0x00, 0x00) // control word: all literals
	data = append(data, payload...)

	return data
}

func TestQlzDecompressErrorTooShort(t *testing.T) {
	q := transport.NewQlz()
	_, err := q.Decompress([]byte{0x04})
	if err == nil {
		t.Error("expected error for too-short data (< 3 bytes)")
	}
}

func TestQlzDecompressErrorWrongLevel(t *testing.T) {
	q := transport.NewQlz()
	// flags=0x08: level=(0x08>>2)&0x03=2 (unsupported)
	_, err := q.Decompress([]byte{0x08, 0x00, 0x04})
	if err == nil {
		t.Error("expected error for non-level-1 data")
	}
}

func TestQlzDecompressUncompressed3ByteHeader(t *testing.T) {
	payload := []byte("ABCD")
	q := transport.NewQlz()
	result, err := q.Decompress(buildUncompressedQlz(payload))
	if err != nil {
		t.Fatalf("Decompress failed: %v", err)
	}
	if !bytes.Equal(result, payload) {
		t.Errorf("result=%v, want %v", result, payload)
	}
}

func TestQlzDecompressUncompressed9ByteHeader(t *testing.T) {
	// flags=0x06: level=1, 9-byte header (bit 1=1), uncompressed (bit 0=0).
	// Decompressed size is uint32 LE at bytes [5:9].
	payload := []byte("ABCD")
	data := make([]byte, 9+len(payload))
	data[0] = 0x06
	data[5] = byte(len(payload))
	copy(data[9:], payload)
	q := transport.NewQlz()
	result, err := q.Decompress(data)
	if err != nil {
		t.Fatalf("Decompress failed: %v", err)
	}
	if !bytes.Equal(result, payload) {
		t.Errorf("result=%v, want %v", result, payload)
	}
}

func TestQlzDecompressCompressedAllLiterals(t *testing.T) {
	// 13 bytes: max(13,10)-10=3 normal-literal iterations,
	// then the remaining 10 go through the near-end literal path.
	payload := []byte("Hello, World!")
	q := transport.NewQlz()
	result, err := q.Decompress(buildAllLiteralQlz(payload))
	if err != nil {
		t.Fatalf("Decompress failed: %v", err)
	}
	if !bytes.Equal(result, payload) {
		t.Errorf("result=%q, want %q", result, payload)
	}
}

func TestQlzDecompressCompressedShortPayload(t *testing.T) {
	// Payload shorter than 10 bytes: all iterations use the near-end path.
	payload := []byte("Hi!")
	q := transport.NewQlz()
	result, err := q.Decompress(buildAllLiteralQlz(payload))
	if err != nil {
		t.Fatalf("Decompress failed: %v", err)
	}
	if !bytes.Equal(result, payload) {
		t.Errorf("result=%q, want %q", result, payload)
	}
}

func TestQlzDecompressMultipleCalls(t *testing.T) {
	// Verify hashtable is reset between calls (no state leak)
	q := transport.NewQlz()
	for _, payload := range [][]byte{[]byte("first call data"), []byte("second call data")} {
		result, err := q.Decompress(buildAllLiteralQlz(payload))
		if err != nil {
			t.Fatalf("Decompress failed: %v", err)
		}
		if !bytes.Equal(result, payload) {
			t.Errorf("result=%q, want %q", result, payload)
		}
	}
}

func TestQlzDecompressEmptyPayload(t *testing.T) {
	q := transport.NewQlz()
	result, err := q.Decompress(buildUncompressedQlz([]byte{}))
	if err != nil {
		t.Fatalf("Decompress failed: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected empty result, got %v", result)
	}
}
