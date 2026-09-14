package nativewire

import (
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
)

// Sign the exact unsigned Hello plus a transport-bound digest; a recorded proof
// cannot be replayed on another connection, server, protocol or nickname.
func identityProof(binding []byte, hello Hello) ([]byte, error) {
	if len(binding) != 32 || len(hello.PublicKey) != ed25519.PublicKeySize {
		return nil, ErrPacket
	}
	hello.Signature = nil
	frame, err := Pack(HelloKind, 0, hello)
	if err != nil {
		return nil, err
	}
	hash := sha256.New()
	hash.Write([]byte("resona-client-identity-v1\x00"))
	hash.Write(binding)
	hash.Write(frame)
	return hash.Sum(nil), nil
}

func SignHello(c Connection, key ed25519.PrivateKey, hello *Hello) error {
	if len(key) != ed25519.PrivateKeySize || hello == nil {
		return ErrPacket
	}
	binding, err := ChannelBinding(c)
	if err != nil {
		return err
	}
	hello.PublicKey = append([]byte(nil), key.Public().(ed25519.PublicKey)...)
	proof, err := identityProof(binding, *hello)
	if err != nil {
		return err
	}
	hello.Signature = ed25519.Sign(key, proof)
	return nil
}

func VerifyHello(c Connection, hello Hello) error {
	binding, err := ChannelBinding(c)
	if err != nil {
		return err
	}
	proof, err := identityProof(binding, hello)
	if err != nil || len(hello.Signature) != ed25519.SignatureSize || !ed25519.Verify(hello.PublicKey, proof, hello.Signature) {
		return errors.New("native identity authentication failed")
	}
	return nil
}
