package crypto

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"errors"

	"github.com/oasisprotocol/curve25519-voi/curve"
	"github.com/oasisprotocol/curve25519-voi/curve/scalar"
)

var errInvalidKeyLength = errors.New("invalid key length")

const (
	curve25519KeySize = 32
	clampMaskLow      = 248
	clampMaskHigh     = 127
	clampHighBit      = 64
	sharedSignBit     = 0x80
	privateKeyTopMask = 0x7F
)

func GenerateTemporaryKey() ([]byte, []byte, error) {
	privateKey := make([]byte, curve25519KeySize)
	_, err := rand.Read(privateKey)
	if err != nil {
		return nil, nil, err
	}
	ClampScalar(privateKey)
	sc, err := scalar.NewFromBits(privateKey)
	if err != nil {
		return nil, nil, err
	}
	publicKey, err := curve.NewEdwardsPoint().MulBasepoint(curve.ED25519_BASEPOINT_TABLE, sc).MarshalBinary()
	if err != nil {
		return nil, nil, err
	}

	return publicKey, privateKey, nil
}

func Sign(priv *ecdsa.PrivateKey, data []byte) ([]byte, error) {
	hash := sha256.Sum256(data)

	return ecdsa.SignASN1(rand.Reader, priv, hash[:])
}

func VerifySign(pub *ecdsa.PublicKey, data, sig []byte) bool {
	hash := sha256.Sum256(data)

	return ecdsa.VerifyASN1(pub, hash[:], sig)
}

func ClampScalar(key []byte) {
	if len(key) < curve25519KeySize {
		return
	}
	key[0] &= clampMaskLow
	key[curve25519KeySize-1] &= clampMaskHigh
	key[curve25519KeySize-1] |= clampHighBit
}

func GetSharedSecret2(publicKey, privateKey []byte) ([]byte, error) {
	if len(publicKey) != curve25519KeySize || len(privateKey) != curve25519KeySize {
		return nil, errInvalidKeyLength
	}

	privateKeyCpy := make([]byte, curve25519KeySize)
	copy(privateKeyCpy, privateKey)
	privateKeyCpy[curve25519KeySize-1] &= privateKeyTopMask
	sc, err := scalar.NewFromBits(privateKeyCpy)
	if err != nil {
		return nil, err
	}

	pub := curve.NewEdwardsPoint()
	err = pub.UnmarshalBinary(publicKey)
	if err != nil {
		return nil, err
	}
	pub.Neg(pub)

	sharedPoint := curve.NewEdwardsPoint().Mul(pub, sc)
	shared, err := sharedPoint.MarshalBinary()
	if err != nil {
		return nil, err
	}
	shared[curve25519KeySize-1] ^= sharedSignBit

	hash := sha512.Sum512(shared)

	return hash[:], nil
}
