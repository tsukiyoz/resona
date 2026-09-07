package handshake_test

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"strings"
	"testing"

	"github.com/honeybbq/teamspeak-go/commands"
	"github.com/honeybbq/teamspeak-go/crypto"
	"github.com/honeybbq/teamspeak-go/handshake"
)

const (
	versionLen  = 4
	initTypeLen = 1
)

// testIdentity is a fixed, low-security-level identity for tests.
const testIdentityStr = "W2OSGpWxkzBPJjt8iyJFsMnqnwHCnxOlmE9gWFOFnKs=:0"

func newTestCrypt(t *testing.T) *crypto.Crypt {
	t.Helper()
	id, err := crypto.IdentityFromString(testIdentityStr)
	if err != nil {
		t.Fatalf("IdentityFromString failed: %v", err)
	}

	return crypto.NewCrypt(id)
}

func TestProcessInit1Start_NilData(t *testing.T) {
	tc := newTestCrypt(t)
	out := handshake.ProcessInit1(tc, nil)
	if out == nil {
		t.Fatal("expected non-nil output for nil data (Start)")
	}
	// 21 bytes: 4 (version) + 1 (type 0x00) + 4 (timestamp) + 4 (rand) + 8 (padding)
	if len(out) != 21 {
		t.Errorf("expected 21 bytes, got %d", len(out))
	}
	ver := binary.BigEndian.Uint32(out[0:4])
	if ver != handshake.InitVersion {
		t.Errorf("expected InitVersion %d, got %d", handshake.InitVersion, ver)
	}
	if out[4] != 0x00 {
		t.Errorf("expected step byte 0x00, got 0x%02x", out[4])
	}
}

func TestProcessInit1Start_RestartByte(t *testing.T) {
	tc := newTestCrypt(t)
	out := handshake.ProcessInit1(tc, []byte{0x7F})
	if out == nil {
		t.Fatal("expected non-nil output for 0x7F restart byte")
	}
	if len(out) != 21 {
		t.Errorf("expected 21 bytes, got %d", len(out))
	}
	if out[4] != 0x00 {
		t.Errorf("expected step byte 0x00, got 0x%02x", out[4])
	}
}

func TestProcessInit1Step0(t *testing.T) {
	tc := newTestCrypt(t)

	// Craft a valid 21-byte step-0 input (simulating server → client).
	// Layout: [1 type=0][...][4 tsRand LE at bytes 9-12][...]
	// ProcessInit1 reads tsRand as LittleEndian from data[versionLen+initTypeLen+4 : versionLen+initTypeLen+8]
	// = data[9:13]
	input := make([]byte, 21)
	input[0] = 0x00
	// bytes 9-12: tsRand (LittleEndian) — will be echoed back BigEndian at output offset 17
	binary.LittleEndian.PutUint32(input[9:13], 0xDEADBEEF)

	out := handshake.ProcessInit1(tc, input)
	if out == nil {
		t.Fatal("expected non-nil output for step 0")
	}
	// 21 bytes: [1 type=1][16 zeros][4 tsRand BE]
	if len(out) != 21 {
		t.Errorf("expected 21 bytes output, got %d", len(out))
	}
	if out[0] != 0x01 {
		t.Errorf("expected step type 0x01, got 0x%02x", out[0])
	}
	echoed := binary.BigEndian.Uint32(out[17:21])
	if echoed != 0xDEADBEEF {
		t.Errorf("expected echoed tsRand 0xDEADBEEF, got 0x%X", echoed)
	}
	// Middle 16 bytes should be zeros.
	if !bytes.Equal(out[1:17], make([]byte, 16)) {
		t.Error("expected zeros in bytes 1:17")
	}
}

func TestProcessInit1Step0_WrongLength(t *testing.T) {
	tc := newTestCrypt(t)
	out := handshake.ProcessInit1(tc, []byte{0x00, 0x01}) // only 2 bytes
	if out != nil {
		t.Error("expected nil output for wrong-length step 0")
	}
}

func TestProcessInit1Step1(t *testing.T) {
	tc := newTestCrypt(t)

	// Valid 21-byte step-1 input.
	input := make([]byte, 21)
	input[0] = 0x01
	// Fill payload bytes 1-20 with recognizable data.
	for i := 1; i < 21; i++ {
		input[i] = byte(i)
	}

	out := handshake.ProcessInit1(tc, input)
	if out == nil {
		t.Fatal("expected non-nil output for step 1")
	}
	// 25 bytes: [4 version][1 type=2][20 echo of input[1:21]]
	if len(out) != 25 {
		t.Errorf("expected 25 bytes output, got %d", len(out))
	}
	ver := binary.BigEndian.Uint32(out[0:4])
	if ver != handshake.InitVersion {
		t.Errorf("expected InitVersion %d, got %d", handshake.InitVersion, ver)
	}
	if out[4] != 0x02 {
		t.Errorf("expected step byte 0x02, got 0x%02x", out[4])
	}
	if !bytes.Equal(out[5:25], input[1:21]) {
		t.Error("expected echo of input[1:21] in output[5:25]")
	}
}

func TestProcessInit1Step1_WrongLength(t *testing.T) {
	tc := newTestCrypt(t)
	out := handshake.ProcessInit1(tc, []byte{0x01}) // only 1 byte
	if out != nil {
		t.Error("expected nil output for wrong-length step 1")
	}
}

func TestProcessInit1Step2(t *testing.T) {
	tc := newTestCrypt(t)

	// Valid step-2 input: exactly versionLen+initTypeLen+16+4 = 25 bytes.
	input := make([]byte, 25)
	input[0] = 0x02

	out := handshake.ProcessInit1(tc, input)
	if out == nil {
		t.Fatal("expected non-nil output for step 2")
	}
	// 133 bytes: [1 type=3][64 x with last byte=1][64 n with last byte=1][4 BE uint=1][100 zeros]
	expectedLen := initTypeLen + 64 + 64 + 4 + 100
	if len(out) != expectedLen {
		t.Errorf("expected %d bytes output, got %d", expectedLen, len(out))
	}
	if out[0] != 0x03 {
		t.Errorf("expected step byte 0x03, got 0x%02x", out[0])
	}
	if out[initTypeLen+64-1] != 1 {
		t.Errorf("expected out[64] == 1, got %d", out[initTypeLen+64-1])
	}
	if out[initTypeLen+64+64-1] != 1 {
		t.Errorf("expected out[128] == 1, got %d", out[initTypeLen+64+64-1])
	}
	level := binary.BigEndian.Uint32(out[initTypeLen+128 : initTypeLen+128+4])
	if level != 1 {
		t.Errorf("expected level=1, got %d", level)
	}
}

func TestProcessInit1Step2_WrongLength(t *testing.T) {
	tc := newTestCrypt(t)
	out := handshake.ProcessInit1(tc, []byte{0x02, 0x00}) // too short
	if out != nil {
		t.Error("expected nil output for wrong-length step 2")
	}
}

// buildStep3Input builds a 233-byte step-3 input with level=0 (instant RSA solve).
func buildStep3Input() []byte {
	// 233 bytes: [1 type=3][64 x][64 n][4 level][100 padding]
	input := make([]byte, 233)
	input[0] = 0x03

	// x = 2 (at offset 1, 64 bytes big-endian)
	input[1+63] = 0x02 // last byte of 64-byte big-endian x

	// n = 15 (at offset 65, 64 bytes big-endian) — small modulus, level=0 → y=x=2
	input[1+64+63] = 0x0F

	// level = 0 → y = x^(2^0) mod n = x^1 mod n = 2
	binary.BigEndian.PutUint32(input[1+128:1+132], 0)

	return input
}

func TestProcessInit1Step3_Level0(t *testing.T) {
	tc := newTestCrypt(t)
	input := buildStep3Input()

	out := handshake.ProcessInit1(tc, input)
	if out == nil {
		t.Fatal("expected non-nil output for step 3 with level=0")
	}

	// Output: [4 version][1 type=0x04][232 data from input[1:233]][64 y][cmdBytes]
	minLen := versionLen + initTypeLen + 232 + 64
	if len(out) < minLen {
		t.Fatalf("expected at least %d bytes output, got %d", minLen, len(out))
	}
	ver := binary.BigEndian.Uint32(out[0:4])
	if ver != handshake.InitVersion {
		t.Errorf("expected InitVersion, got %d", ver)
	}
	if out[4] != 0x04 {
		t.Errorf("expected step byte 0x04, got 0x%02x", out[4])
	}
	if !bytes.Equal(out[5:5+232], input[1:233]) {
		t.Error("expected input[1:233] echoed in output[5:237]")
	}

	assertStep3ClientInitIV(t, tc, out)
}

func assertStep3ClientInitIV(t *testing.T, tc *crypto.Crypt, out []byte) {
	t.Helper()

	if len(tc.AlphaTmp) != 10 {
		t.Errorf("expected AlphaTmp length 10, got %d", len(tc.AlphaTmp))
	}

	cmdPart := string(out[5+232+64:])
	cmd := commands.ParseCommand(cmdPart)
	if cmd == nil || cmd.Name != "clientinitiv" {
		t.Errorf("expected clientinitiv command, got %q", cmdPart)

		return
	}
	if cmd.Params["ot"] != "1" {
		t.Errorf("expected ot=1, got %q", cmd.Params["ot"])
	}
	alphaB64 := cmd.Params["alpha"]
	alphaBytes, err := base64.StdEncoding.DecodeString(alphaB64)
	if err != nil || len(alphaBytes) != 10 {
		t.Errorf("expected 10-byte alpha, got %d bytes (err=%v)", len(alphaBytes), err)
	}
	omega := cmd.Params["omega"]
	if len(omega) < 20 {
		t.Errorf("omega looks too short: %q", omega)
	}
	if !strings.HasSuffix(omega, "=") && len(omega)%4 != 0 {
		t.Errorf("omega is not valid base64: %q", omega)
	}
}

func TestProcessInit1Step3_WrongLength(t *testing.T) {
	tc := newTestCrypt(t)
	out := handshake.ProcessInit1(tc, []byte{0x03, 0x00}) // too short
	if out != nil {
		t.Error("expected nil output for wrong-length step 3")
	}
}

func TestProcessInit1Step3_LevelOutOfRange(t *testing.T) {
	tc := newTestCrypt(t)
	input := buildStep3Input()
	// Set level = 2000000 (exceeds the 1000000 limit)
	binary.BigEndian.PutUint32(input[1+128:1+132], 2000000)
	out := handshake.ProcessInit1(tc, input)
	if out != nil {
		t.Error("expected nil output for RSA level out of range")
	}
}

func TestProcessInit1UnknownStep(t *testing.T) {
	tc := newTestCrypt(t)
	out := handshake.ProcessInit1(tc, []byte{0x05})
	if out != nil {
		t.Error("expected nil output for unknown step byte")
	}
}
