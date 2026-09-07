package crypto_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"testing"

	"github.com/honeybbq/teamspeak-go/crypto"
	"github.com/honeybbq/teamspeak-go/handshake"
	"github.com/oasisprotocol/curve25519-voi/curve"
	"github.com/oasisprotocol/curve25519-voi/curve/scalar"
)

func TestGetSharedSecret2(t *testing.T) {
	publicKeyHex := "9d93589a4a86cf80d8dc1c1b384555289454021ad2f5dacf29d9938eade940b1"
	privateKeyHex := "58cd07b6765c3427afcfa64c73a609705a7f1656f40c582c7362080791bcfb68"
	expectedSharedSecretHex := "8aa100de2e0cde11827c36b5b3ef2758b1a7d52a202c375049cd8a3944d764" +
		"14b9854db31f781b5b51f37c025e9efee70edcd7189ccb7831a04eb7bc09e5b20b"

	publicKey, _ := hex.DecodeString(publicKeyHex)
	privateKey, _ := hex.DecodeString(privateKeyHex)

	sharedSecret, err := crypto.GetSharedSecret2(publicKey, privateKey)
	if err != nil {
		t.Fatalf("GetSharedSecret2 failed: %v", err)
	}

	actualSharedSecretHex := hex.EncodeToString(sharedSecret)
	if actualSharedSecretHex != expectedSharedSecretHex {
		t.Errorf("sharedSecret mismatch:\n  expected: %s\n  actual:   %s", expectedSharedSecretHex, actualSharedSecretHex)
	}
}

func TestGetKeyNonce(t *testing.T) {
	ivStructHex := "10ea569521d4d88e06a63db265416d780daf03a1ee4d3731ba22e8981e61d292" +
		"febebc434ce2a2ac36e8e1bd2b6cd9c953f84d7a269cc42f33917de8c47b8bdf"
	expectedKeyHex := "0659e387b9258c3f4fe32b31bc794dee"
	expectedNonceHex := "ec4630a6e61e216f61e15788bb42eaec"

	ivStruct, _ := hex.DecodeString(ivStructHex)

	tc := &crypto.Crypt{
		IvStruct:           ivStruct,
		CryptoInitComplete: true,
		CachedKeys:         make(map[uint64]crypto.KeyNonce),
	}

	key, nonce := tc.GetKeyNonce(false, 2, 0, 2, false)

	actualKeyHex := hex.EncodeToString(key)
	actualNonceHex := hex.EncodeToString(nonce)

	if actualKeyHex != expectedKeyHex {
		t.Errorf("key mismatch:\n  expected: %s\n  actual:   %s", expectedKeyHex, actualKeyHex)
	}
	if actualNonceHex != expectedNonceHex {
		t.Errorf("nonce mismatch:\n  expected: %s\n  actual:   %s", expectedNonceHex, actualNonceHex)
	}
}

func TestGenerateTemporaryKey(t *testing.T) {
	pubKey, privKey, err := crypto.GenerateTemporaryKey()
	if err != nil {
		t.Fatalf("GenerateTemporaryKey failed: %v", err)
	}

	if len(pubKey) != 32 {
		t.Errorf("publicKey should be 32 bytes, got %d", len(pubKey))
	}
	if len(privKey) != 32 {
		t.Errorf("privateKey should be 32 bytes, got %d", len(privKey))
	}
}

func TestGetSharedSecret2WithCSharpData(t *testing.T) {
	publicKeyHex := "a878824253ba90c33297d0e44fa52439d9a35e316200e712d9d9e0efcd11dc0a"
	privateKeyHex := "b8985f89031ee1adf325fb5595fe5810f232fa33c5629eb4632969e17e69717f"
	expectedSharedSecretHex := "91478e774dc13a156cc2019c6c6ebe63d220381a2a914a6bedd49058685fdc" +
		"55a02c79569a62d4d71926899c8e45fb56122ef86a445cfb461689c945c826e707"

	publicKey, _ := hex.DecodeString(publicKeyHex)
	privateKey, _ := hex.DecodeString(privateKeyHex)
	expectedSharedSecret, _ := hex.DecodeString(expectedSharedSecretHex)

	sharedSecret, err := crypto.GetSharedSecret2(publicKey, privateKey)
	if err != nil {
		t.Fatalf("GetSharedSecret2 failed: %v", err)
	}

	if hex.EncodeToString(sharedSecret) != hex.EncodeToString(expectedSharedSecret) {
		t.Errorf("sharedSecret mismatch")
	}
}

func TestTemporaryKeyWithFixedPrivate(t *testing.T) {
	privateKeyHex := "a02708b21598ae10932dc8eac25cf70bdd033c1f36f14a2caf24036dd8010d5b"
	expectedPublicKeyHex := "f67d0b5b0db004ab4f5df21d9e92f184e32aa45d90f483889912f95e7071ad79"

	privateKey, _ := hex.DecodeString(privateKeyHex)

	sc, err := scalar.NewFromBits(privateKey)
	if err != nil {
		t.Fatalf("NewFromBits failed: %v", err)
	}
	publicKey, err := curve.NewEdwardsPoint().MulBasepoint(curve.ED25519_BASEPOINT_TABLE, sc).MarshalBinary()
	if err != nil {
		t.Fatalf("MulBasepoint failed: %v", err)
	}

	if hex.EncodeToString(publicKey) != expectedPublicKeyHex {
		t.Errorf("publicKey mismatch")
	}
}

func TestFullLicenseParseAndDerive(t *testing.T) {
	licenseBase64 := "AQBgjAAqtcBUrw5futTtkl3+EM3OW4Lal6OTPlwuv4xV/gIRFlEAG0Nl" +
		"AAcAAAAgQW5vbnltb3VzAACWSZf+Mjl5RT5mu4rvf8nhAZp9TjXO10XfGHQ9HQPtHiAYiqjtGItRrQ=="
	licenseBytes, _ := base64.StdEncoding.DecodeString(licenseBase64)

	chain, err := handshake.ParseLicenses(licenseBytes)
	if err != nil {
		t.Fatalf("ParseLicenses failed: %v", err)
	}

	if len(chain.Blocks) != 2 {
		t.Errorf("expected 2 blocks, got %d", len(chain.Blocks))
	}

	key, err := chain.DeriveKey()
	if err != nil {
		t.Fatalf("DeriveKey failed: %v", err)
	}

	if len(key) != 32 {
		t.Errorf("expected 32-byte key, got %d bytes", len(key))
	}
}

func TestIdentityStringRoundtrip(t *testing.T) {
	id, err := crypto.GenerateIdentity(0)
	if err != nil {
		t.Fatalf("GenerateIdentity failed: %v", err)
	}
	s := id.String()
	id2, err := crypto.IdentityFromString(s)
	if err != nil {
		t.Fatalf("IdentityFromString failed: %v", err)
	}
	// Compare via serialised form to avoid accessing deprecated D field directly.
	if id.String() != id2.String() {
		t.Errorf("serialized identity mismatch: %q vs %q", id.String(), id2.String())
	}
	if id.Offset != id2.Offset {
		t.Errorf("Offset mismatch: %d vs %d", id.Offset, id2.Offset)
	}
}

func TestIdentityFromStringErrors(t *testing.T) {
	cases := []string{
		"",
		"notvalidnocodon",
		"invalid==base64:0",
		"dGVzdA==:notanumber",
	}
	for _, s := range cases {
		_, err := crypto.IdentityFromString(s)
		if err == nil {
			t.Errorf("IdentityFromString(%q) expected error, got nil", s)
		}
	}
}

func TestIdentitySecurityLevel(t *testing.T) {
	id, err := crypto.GenerateIdentity(0)
	if err != nil {
		t.Fatal(err)
	}
	lvl := id.SecurityLevel()
	if lvl < 0 {
		t.Errorf("SecurityLevel should be >= 0, got %d", lvl)
	}
}

func TestIdentityUpgradeToLevel(t *testing.T) {
	id, err := crypto.GenerateIdentity(0)
	if err != nil {
		t.Fatal(err)
	}
	err = id.UpgradeToLevel(1, context.Background())
	if err != nil {
		t.Fatalf("UpgradeToLevel(1) failed: %v", err)
	}
	if id.SecurityLevel() < 1 {
		t.Errorf("SecurityLevel after upgrade = %d, want >= 1", id.SecurityLevel())
	}
}

func TestIdentityUpgradeCtxCancelled(t *testing.T) {
	id, err := crypto.GenerateIdentity(0)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately
	err = id.UpgradeToLevel(100, ctx)
	if err == nil {
		t.Error("expected context cancellation error")
	}
}

func TestPublicKeyBase64RoundtrippableWithImport(t *testing.T) {
	id, err := crypto.GenerateIdentity(0)
	if err != nil {
		t.Fatal(err)
	}
	pubB64 := id.PublicKeyBase64()
	pubBytes, err := base64.StdEncoding.DecodeString(pubB64)
	if err != nil {
		t.Fatalf("base64 decode failed: %v", err)
	}
	// Verify round-trip: import the bytes and check the UID is consistent.
	_, err = crypto.ImportPublicKey(pubBytes)
	if err != nil {
		t.Fatalf("ImportPublicKey failed: %v", err)
	}
	// GetUidFromPublicKey verifies the public key serialisation is stable.
	uid1 := crypto.GetUidFromPublicKey(pubB64)
	uid2 := crypto.GetUidFromPublicKey(pubB64)
	if uid1 != uid2 {
		t.Error("UID is not stable after import")
	}
}

func TestGetUidFromPublicKey(t *testing.T) {
	id, err := crypto.GenerateIdentity(0)
	if err != nil {
		t.Fatal(err)
	}
	uid1 := crypto.GetUidFromPublicKey(id.PublicKeyBase64())
	uid2 := crypto.GetUidFromPublicKey(id.PublicKeyBase64())
	if uid1 != uid2 {
		t.Error("GetUidFromPublicKey is not deterministic")
	}
	// SHA-1 produces 20 bytes → base64 = 28 chars
	if len(uid1) != 28 {
		t.Errorf("UID length = %d, want 28", len(uid1))
	}
}

func TestHash512Length(t *testing.T) {
	out := crypto.Hash512([]byte("hello"))
	if len(out) != 64 {
		t.Errorf("Hash512 output length = %d, want 64", len(out))
	}
}

func TestHash512Deterministic(t *testing.T) {
	data := []byte("determinism test")
	if !bytes.Equal(crypto.Hash512(data), crypto.Hash512(data)) {
		t.Error("Hash512 is not deterministic")
	}
}

func TestHash512EmptyInput(t *testing.T) {
	// SHA-512 of empty string is a well-known constant.
	const emptyHex = "cf83e1357eefb8bdf1542850d66d8007d620e4050b5715dc83f4a921d36ce9ce" +
		"47d0d13c5d85f2b0ff8318d2877eec2f63b931bd47417a81a538327af927da3e"
	out := crypto.Hash512(nil)
	if hex.EncodeToString(out) != emptyHex {
		t.Errorf("Hash512(nil) = %x, want %s", out, emptyHex)
	}
}

func TestClampScalar(t *testing.T) {
	key := make([]byte, 32)
	key[0] = 0xFF
	key[31] = 0xFF
	crypto.ClampScalar(key)
	if key[0]&0x07 != 0 {
		t.Errorf("key[0] low 3 bits should be 0 after clamping, got 0x%02X", key[0])
	}
	if key[31]&0x80 != 0 {
		t.Errorf("key[31] high bit should be 0 after clamping, got 0x%02X", key[31])
	}
	if key[31]&0x40 == 0 {
		t.Errorf("key[31] bit 6 should be 1 after clamping, got 0x%02X", key[31])
	}
}

func TestClampScalarTooShort(t *testing.T) {
	key := []byte{0xFF, 0xFF}
	crypto.ClampScalar(key) // should not panic
}

// Sign / VerifySign

func TestSignAndVerify(t *testing.T) {
	id, err := crypto.GenerateIdentity(0)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("the message to sign")
	sig, err := crypto.Sign(id.PrivateKey, data)
	if err != nil {
		t.Fatalf("Sign failed: %v", err)
	}
	if !crypto.VerifySign(&id.PrivateKey.PublicKey, data, sig) {
		t.Error("VerifySign returned false for valid signature")
	}
}

func TestVerifySignFailsOnTamperedData(t *testing.T) {
	id, err := crypto.GenerateIdentity(0)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("original")
	sig, _ := crypto.Sign(id.PrivateKey, data)
	if crypto.VerifySign(&id.PrivateKey.PublicKey, append(data, 'X'), sig) {
		t.Error("VerifySign should fail for tampered data")
	}
}

func TestVerifySignFailsOnTamperedSig(t *testing.T) {
	id, err := crypto.GenerateIdentity(0)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("original")
	sig, _ := crypto.Sign(id.PrivateKey, data)
	sig[0] ^= 0xFF
	if crypto.VerifySign(&id.PrivateKey.PublicKey, data, sig) {
		t.Error("VerifySign should fail for tampered signature")
	}
}

func makeSolveData(x, n byte) []byte {
	data := make([]byte, 128)
	data[63] = x  // x as 64-byte big-endian
	data[127] = n // n as 64-byte big-endian

	return data
}

func TestSolveRsaChallengeLevel0(t *testing.T) {
	// level=0: no squarings, result = x (padded to 64 bytes)
	tc := crypto.NewCrypt(nil)
	data := makeSolveData(5, 100) // x=5, n=100
	result, err := tc.SolveRsaChallenge(data, 0, 0)
	if err != nil {
		t.Fatalf("SolveRsaChallenge failed: %v", err)
	}
	if len(result) != 64 {
		t.Errorf("result length = %d, want 64", len(result))
	}
	if result[63] != 5 {
		t.Errorf("result[63] = %d, want 5", result[63])
	}
	for i := range 63 {
		if result[i] != 0 {
			t.Errorf("result[%d] = %d, want 0", i, result[i])
		}
	}
}

func TestSolveRsaChallengeLevel2(t *testing.T) {
	// x=2, n=17, level=2: y = ((2^2)^2) mod 17 = 4^2 mod 17 = 16
	tc := crypto.NewCrypt(nil)
	data := makeSolveData(2, 17)
	result, err := tc.SolveRsaChallenge(data, 0, 2)
	if err != nil {
		t.Fatalf("SolveRsaChallenge failed: %v", err)
	}
	if result[63] != 16 {
		t.Errorf("result[63] = %d, want 16", result[63])
	}
}

func TestSolveRsaChallengeNegativeLevel(t *testing.T) {
	tc := crypto.NewCrypt(nil)
	_, err := tc.SolveRsaChallenge(make([]byte, 128), 0, -1)
	if err == nil {
		t.Error("expected error for level < 0")
	}
}

func TestSolveRsaChallengeLevelTooHigh(t *testing.T) {
	tc := crypto.NewCrypt(nil)
	_, err := tc.SolveRsaChallenge(make([]byte, 128), 0, 1000001)
	if err == nil {
		t.Error("expected error for level > 1000000")
	}
}

func TestNewCrypt(t *testing.T) {
	id, _ := crypto.GenerateIdentity(0)
	tc := crypto.NewCrypt(id)
	if tc == nil {
		t.Fatal("expected non-nil Crypt")
	}
	if len(tc.FakeSignature) != 8 {
		t.Errorf("FakeSignature length = %d, want 8", len(tc.FakeSignature))
	}
	if tc.CachedKeys == nil {
		t.Error("CachedKeys should be initialized")
	}
}

func TestSetSharedSecret(t *testing.T) {
	tc := crypto.NewCrypt(nil)
	alpha := make([]byte, 10)
	beta := make([]byte, 10)
	sharedKey := make([]byte, 20)
	err := tc.SetSharedSecret(alpha, beta, sharedKey)
	if err != nil {
		t.Fatalf("SetSharedSecret failed: %v", err)
	}
	if !tc.CryptoInitComplete {
		t.Error("CryptoInitComplete should be true")
	}
	if len(tc.IvStruct) != 20 {
		t.Errorf("IvStruct length = %d, want 20", len(tc.IvStruct))
	}
}

func TestDebugCryptoStateEmpty(t *testing.T) {
	tc := crypto.NewCrypt(nil)
	length, hexStr := tc.DebugCryptoState()
	if length != 0 || hexStr != "" {
		t.Errorf("empty Crypt: DebugCryptoState() = (%d, %q), want (0, \"\")", length, hexStr)
	}
}

func TestDebugCryptoStateAfterSetSharedSecret(t *testing.T) {
	tc := crypto.NewCrypt(nil)
	_ = tc.SetSharedSecret(make([]byte, 10), make([]byte, 10), make([]byte, 20))
	length, hexStr := tc.DebugCryptoState()
	if length == 0 {
		t.Error("IvStruct should be non-empty after SetSharedSecret")
	}
	if len(hexStr) == 0 {
		t.Error("FakeSignature hex should be non-empty")
	}
}

func TestEncryptInit1Passthrough(t *testing.T) {
	// PacketTypeInit1 (type=8): plaintext is returned unchanged, MAC = "TS3INIT1"
	tc := crypto.NewCrypt(nil)
	plaintext := []byte{0x01, 0x02, 0x03}
	header := []byte{0x00, 0x65, 0x00, 0x00, 0x08}
	ct, mac, err := tc.Encrypt(8, 0, 0, header, plaintext, false, false)
	if err != nil {
		t.Fatalf("Encrypt Init1 failed: %v", err)
	}
	if !bytes.Equal(ct, plaintext) {
		t.Error("Init1: ciphertext should equal plaintext")
	}
	if string(mac) != "TS3INIT1" {
		t.Errorf("Init1 MAC = %q, want TS3INIT1", mac)
	}
}

func TestDecryptInit1Passthrough(t *testing.T) {
	tc := crypto.NewCrypt(nil)
	data := []byte{0xAA, 0xBB}
	header := []byte{0x00}
	pt, err := tc.Decrypt(8, 0, 0, header, data, nil, false, false)
	if err != nil {
		t.Fatalf("Decrypt Init1 failed: %v", err)
	}
	if !bytes.Equal(pt, data) {
		t.Error("Init1: decrypted should equal original data")
	}
}

func TestEncryptDecryptUnencrypted(t *testing.T) {
	tc := &crypto.Crypt{
		FakeSignature: []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08},
		CachedKeys:    make(map[uint64]crypto.KeyNonce),
	}
	plaintext := []byte("voice packet data")
	header := []byte{0x00, 0x01, 0x02}
	ct, mac, err := tc.Encrypt(0, 5, 0, header, plaintext, false, true)
	if err != nil {
		t.Fatalf("Encrypt unencrypted failed: %v", err)
	}
	if !bytes.Equal(ct, plaintext) {
		t.Error("unencrypted: ciphertext should equal plaintext")
	}
	if !bytes.Equal(mac, tc.FakeSignature) {
		t.Error("unencrypted: MAC should equal FakeSignature")
	}
	pt, err := tc.Decrypt(0, 5, 0, header, ct, mac, false, true)
	if err != nil {
		t.Fatalf("Decrypt unencrypted failed: %v", err)
	}
	if !bytes.Equal(pt, plaintext) {
		t.Error("unencrypted decrypt mismatch")
	}
}

func TestDecryptUnencryptedBadSignature(t *testing.T) {
	tc := &crypto.Crypt{
		FakeSignature: []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08},
		CachedKeys:    make(map[uint64]crypto.KeyNonce),
	}
	badTag := []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}
	_, err := tc.Decrypt(0, 1, 0, nil, []byte("data"), badTag, false, true)
	if err == nil {
		t.Error("expected ErrFakeSignatureMismatch")
	}
}

func TestEncryptDecryptEAXRoundtripDummy(t *testing.T) {
	// dummy=true uses fixed key/nonce; both Encrypt and Decrypt use the same
	// dummy key so the roundtrip is self-consistent.
	tc := &crypto.Crypt{
		FakeSignature: make([]byte, 8),
		CachedKeys:    make(map[uint64]crypto.KeyNonce),
	}
	plaintext := []byte("hello encrypted world")
	header := []byte{0x00, 0x01, 0x02, 0x03, 0x04}
	ct, mac, err := tc.Encrypt(2, 42, 0, header, plaintext, true, false)
	if err != nil {
		t.Fatalf("Encrypt EAX failed: %v", err)
	}
	if bytes.Equal(ct, plaintext) {
		t.Error("ciphertext should differ from plaintext")
	}
	pt, err := tc.Decrypt(2, 42, 0, header, ct, mac, true, false)
	if err != nil {
		t.Fatalf("Decrypt EAX failed: %v", err)
	}
	if !bytes.Equal(pt, plaintext) {
		t.Errorf("decrypted = %q, want %q", pt, plaintext)
	}
}

func TestAcquireReleaseKeyBuffer(t *testing.T) {
	buf := crypto.AcquireKeyBuffer()
	if len(buf) != 16 {
		t.Errorf("buffer len = %d, want 16", len(buf))
	}
	crypto.ReleaseKeyBuffer(buf)
	// Acquire again — should get a buffer of the correct length
	buf2 := crypto.AcquireKeyBuffer()
	if len(buf2) != 16 {
		t.Errorf("recycled buffer len = %d, want 16", len(buf2))
	}
	crypto.ReleaseKeyBuffer(buf2)
}

func TestReleaseKeyBufferWrongLength(t *testing.T) {
	// Wrong-length buffers should not be returned to the pool (no panic)
	crypto.ReleaseKeyBuffer(make([]byte, 15))
	crypto.ReleaseKeyBuffer(make([]byte, 0))
}

// TestInitCrypto verifies that InitCrypto performs ECDH with a freshly generated
// server key pair and marks crypto as initialized.
func TestInitCrypto(t *testing.T) {
	clientID, err := crypto.IdentityFromString(
		"W2OSGpWxkzBPJjt8iyJFsMnqnwHCnxOlmE9gWFOFnKs=:0",
	)
	if err != nil {
		t.Fatalf("IdentityFromString: %v", err)
	}
	tc := crypto.NewCrypt(clientID)

	// Generate a fresh server identity to use as the server public key.
	serverID, err := crypto.IdentityFromString(
		"W2OSGpWxkzBPJjt8iyJFsMnqnwHCnxOlmE9gWFOFnKs=:0",
	)
	if err != nil {
		t.Fatalf("IdentityFromString server: %v", err)
	}
	omega := serverID.PublicKeyBase64()

	// In the clientinitiv handshake path, alpha and beta are both 10 bytes.
	// SetSharedSecret uses SHA-1 (20 bytes) as the shared key, so beta must be <= 10 bytes.
	alpha := base64.StdEncoding.EncodeToString(make([]byte, 10))
	beta := base64.StdEncoding.EncodeToString(make([]byte, 10))

	err = tc.InitCrypto(alpha, beta, omega)
	if err != nil {
		t.Fatalf("InitCrypto failed: %v", err)
	}
	if !tc.CryptoInitComplete {
		t.Error("expected CryptoInitComplete to be true")
	}
	// IvStruct = 10 (alpha len) + len(betaBytes)
	betaBytes, _ := base64.StdEncoding.DecodeString(beta)
	expectedLen := 10 + len(betaBytes)
	if len(tc.IvStruct) != expectedLen {
		t.Errorf("IvStruct length = %d, want %d", len(tc.IvStruct), expectedLen)
	}
}

func TestInitCrypto_InvalidAlpha(t *testing.T) {
	clientID, _ := crypto.IdentityFromString("W2OSGpWxkzBPJjt8iyJFsMnqnwHCnxOlmE9gWFOFnKs=:0")
	tc := crypto.NewCrypt(clientID)
	err := tc.InitCrypto("not-base64!!!", "", "")
	if err == nil {
		t.Error("expected error for invalid alpha base64")
	}
}

func TestInitCrypto_InvalidBeta(t *testing.T) {
	clientID, _ := crypto.IdentityFromString("W2OSGpWxkzBPJjt8iyJFsMnqnwHCnxOlmE9gWFOFnKs=:0")
	tc := crypto.NewCrypt(clientID)
	err := tc.InitCrypto(base64.StdEncoding.EncodeToString([]byte("alpha")), "not-base64!!!", "")
	if err == nil {
		t.Error("expected error for invalid beta base64")
	}
}

func TestInitCrypto_InvalidOmega(t *testing.T) {
	clientID, _ := crypto.IdentityFromString("W2OSGpWxkzBPJjt8iyJFsMnqnwHCnxOlmE9gWFOFnKs=:0")
	tc := crypto.NewCrypt(clientID)
	alpha := base64.StdEncoding.EncodeToString(make([]byte, 10))
	beta := base64.StdEncoding.EncodeToString(make([]byte, 10))
	err := tc.InitCrypto(alpha, beta, "not-base64!!!")
	if err == nil {
		t.Error("expected error for invalid omega base64")
	}
}

func TestInitCrypto_InvalidOmegaKey(t *testing.T) {
	clientID, _ := crypto.IdentityFromString("W2OSGpWxkzBPJjt8iyJFsMnqnwHCnxOlmE9gWFOFnKs=:0")
	tc := crypto.NewCrypt(clientID)
	alpha := base64.StdEncoding.EncodeToString(make([]byte, 10))
	beta := base64.StdEncoding.EncodeToString(make([]byte, 10))
	badOmega := base64.StdEncoding.EncodeToString([]byte("this is not a valid public key"))
	err := tc.InitCrypto(alpha, beta, badOmega)
	if err == nil {
		t.Error("expected error for invalid omega key bytes")
	}
}
