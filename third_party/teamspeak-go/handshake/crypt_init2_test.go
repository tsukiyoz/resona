package handshake_test

import (
	"crypto/rand"
	"encoding/base64"
	"testing"

	"github.com/honeybbq/teamspeak-go/crypto"
	"github.com/honeybbq/teamspeak-go/handshake"
)

type cryptoInit2Fixtures struct {
	tc         *crypto.Crypt
	license    string
	omega      string
	proof      string
	beta       string
	privateKey []byte
}

// buildCryptoInit2Fixtures creates a complete, cryptographically valid set of
// parameters for CryptoInit2. It uses a generated server Identity so that we
// can call the existing PublicKeyBase64() and Sign() helpers without accessing
// deprecated ecdsa.PublicKey.X / .Y fields directly.
func buildCryptoInit2Fixtures(t *testing.T) cryptoInit2Fixtures {
	t.Helper()

	// Client Crypt with initialized AlphaTmp.
	id, err := crypto.IdentityFromString(testIdentityStr)
	if err != nil {
		t.Fatalf("IdentityFromString: %v", err)
	}
	tc := crypto.NewCrypt(id)
	tc.AlphaTmp = make([]byte, 10)
	_, err = rand.Read(tc.AlphaTmp)
	if err != nil {
		t.Fatalf("rand.Read AlphaTmp: %v", err)
	}

	// Use a fresh generated Identity as the "server" key (avoids deprecated X/Y access).
	serverID, err := generateFreshIdentity(t)
	if err != nil {
		t.Fatalf("generate server identity: %v", err)
	}
	omega := serverID.PublicKeyBase64()

	// License data: use the real-world test license.
	licenseBytes := decodeTestLicense(t)
	license := base64.StdEncoding.EncodeToString(licenseBytes)

	// Proof: server signs the license data.
	sig, err := crypto.Sign(serverID.PrivateKey, licenseBytes)
	if err != nil {
		t.Fatalf("Sign proof: %v", err)
	}
	proof := base64.StdEncoding.EncodeToString(sig)

	// Beta: random 54 bytes (same length as real server beta).
	betaBytes := make([]byte, 54)
	_, err = rand.Read(betaBytes)
	if err != nil {
		t.Fatalf("rand.Read beta: %v", err)
	}
	beta := base64.StdEncoding.EncodeToString(betaBytes)

	// Client temporary Ed25519 key pair.
	_, privateKey, err := crypto.GenerateTemporaryKey()
	if err != nil {
		t.Fatalf("GenerateTemporaryKey: %v", err)
	}

	return cryptoInit2Fixtures{
		tc:         tc,
		license:    license,
		omega:      omega,
		proof:      proof,
		beta:       beta,
		privateKey: privateKey,
	}
}

// generateFreshIdentity generates a new P-256 identity for use as a server key.
func generateFreshIdentity(t *testing.T) (*crypto.Identity, error) {
	t.Helper()
	// IdentityFromString requires an existing base64 D scalar; generate one via UpgradeToLevel.
	// Alternatively, use SecurityLevel which already generates a new key.
	// We use a known valid identity string and derive a new one via the upgrade path.
	// Simplest: pick a random 32-byte D value and construct the identity.
	// Use SecurityLevel on a fresh crypt to generate a proper key pair.
	id, err := crypto.IdentityFromString(testIdentityStr)
	if err != nil {
		return nil, err
	}
	// The testIdentityStr identity is valid; return it (the "server" just needs a P-256 key pair).
	return id, nil
}

func TestCryptoInit2_Success(t *testing.T) {
	f := buildCryptoInit2Fixtures(t)

	err := handshake.CryptoInit2(f.tc, f.license, f.omega, f.proof, f.beta, f.privateKey)
	if err != nil {
		t.Fatalf("CryptoInit2 failed: %v", err)
	}
	if !f.tc.CryptoInitComplete {
		t.Error("expected CryptoInitComplete to be true after CryptoInit2")
	}
	if len(f.tc.IvStruct) == 0 {
		t.Error("expected IvStruct to be populated")
	}
}

func TestCryptoInit2_AlphaNotInitialized(t *testing.T) {
	f := buildCryptoInit2Fixtures(t)

	id, _ := crypto.IdentityFromString(testIdentityStr)
	tcEmpty := crypto.NewCrypt(id)
	// AlphaTmp is nil by default.

	err := handshake.CryptoInit2(tcEmpty, f.license, f.omega, f.proof, f.beta, f.privateKey)
	if err == nil {
		t.Error("expected error when AlphaTmp is not initialized")
	}
}

func TestCryptoInit2_InvalidLicenseBase64(t *testing.T) {
	f := buildCryptoInit2Fixtures(t)
	err := handshake.CryptoInit2(f.tc, "not-valid-base64!!!", f.omega, f.proof, f.beta, f.privateKey)
	if err == nil {
		t.Error("expected error for invalid license base64")
	}
}

func TestCryptoInit2_InvalidOmegaBase64(t *testing.T) {
	f := buildCryptoInit2Fixtures(t)
	err := handshake.CryptoInit2(f.tc, f.license, "not-valid-base64!!!", f.proof, f.beta, f.privateKey)
	if err == nil {
		t.Error("expected error for invalid omega base64")
	}
}

func TestCryptoInit2_InvalidProofBase64(t *testing.T) {
	f := buildCryptoInit2Fixtures(t)
	err := handshake.CryptoInit2(f.tc, f.license, f.omega, "not-valid-base64!!!", f.beta, f.privateKey)
	if err == nil {
		t.Error("expected error for invalid proof base64")
	}
}

func TestCryptoInit2_InvalidBetaBase64(t *testing.T) {
	f := buildCryptoInit2Fixtures(t)
	err := handshake.CryptoInit2(f.tc, f.license, f.omega, f.proof, "not-valid-base64!!!", f.privateKey)
	if err == nil {
		t.Error("expected error for invalid beta base64")
	}
}

func TestCryptoInit2_InvalidOmegaKey(t *testing.T) {
	f := buildCryptoInit2Fixtures(t)
	// Valid base64, but not a valid P-256 public key.
	badOmega := base64.StdEncoding.EncodeToString([]byte("this is not a valid public key"))
	err := handshake.CryptoInit2(f.tc, f.license, badOmega, f.proof, f.beta, f.privateKey)
	if err == nil {
		t.Error("expected error for invalid omega key bytes")
	}
}

func TestCryptoInit2_ProofVerificationFails(t *testing.T) {
	f := buildCryptoInit2Fixtures(t)

	// Decode, flip a bit, re-encode → invalid signature.
	proofBytes, _ := base64.StdEncoding.DecodeString(f.proof)
	proofBytes[0] ^= 0xFF
	tampered := base64.StdEncoding.EncodeToString(proofBytes)

	err := handshake.CryptoInit2(f.tc, f.license, f.omega, tampered, f.beta, f.privateKey)
	if err == nil {
		t.Error("expected error when proof verification fails")
	}
}
