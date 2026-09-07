package handshake

import (
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/honeybbq/teamspeak-go/crypto"
)

var (
	errAlphaNotInitialized = errors.New("alpha is not initialized")
	errInitProofInvalid    = errors.New("init proof is not valid")
)

type init2Payload struct {
	license []byte
	omega   []byte
	proof   []byte
	beta    []byte
}

// CryptoInit2 performs the second stage of crypto initialization (Ed25519 ECDH).
func CryptoInit2(tc *crypto.Crypt, license, omega, proof, beta string, privateKey []byte) error {
	if len(tc.AlphaTmp) == 0 {
		return errAlphaNotInitialized
	}
	payload, err := decodeInit2Payload(license, omega, proof, beta)
	if err != nil {
		return err
	}

	serverPubKey, err := crypto.ImportPublicKey(payload.omega)
	if err != nil {
		return err
	}
	if !crypto.VerifySign(serverPubKey, payload.license, payload.proof) {
		return errInitProofInvalid
	}

	licenses, err := ParseLicenses(payload.license)
	if err != nil {
		return err
	}
	key, err := licenses.DeriveKey()
	if err != nil {
		return err
	}

	sharedSecret, err := crypto.GetSharedSecret2(key, privateKey)
	if err != nil {
		return err
	}

	return tc.SetSharedSecret(tc.AlphaTmp, payload.beta, sharedSecret)
}

func decodeInit2Payload(license, omega, proof, beta string) (*init2Payload, error) {
	licenseBytes, err := base64.StdEncoding.DecodeString(license)
	if err != nil {
		return nil, fmt.Errorf("invalid license: %w", err)
	}
	omegaBytes, err := base64.StdEncoding.DecodeString(omega)
	if err != nil {
		return nil, fmt.Errorf("invalid omega: %w", err)
	}
	proofBytes, err := base64.StdEncoding.DecodeString(proof)
	if err != nil {
		return nil, fmt.Errorf("invalid proof: %w", err)
	}
	betaBytes, err := base64.StdEncoding.DecodeString(beta)
	if err != nil {
		return nil, fmt.Errorf("invalid beta: %w", err)
	}

	return &init2Payload{
		license: licenseBytes,
		omega:   omegaBytes,
		proof:   proofBytes,
		beta:    betaBytes,
	}, nil
}
