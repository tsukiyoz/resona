package crypto

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha512"
	"encoding/asn1"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"sync"
)

var (
	errInvalidIdentityFormat = errors.New("invalid identity format")
	errRSAChallengeOutRange  = errors.New("RSA challenge level out of range")
	errInvalidPublicPoint    = errors.New("invalid public key point encoding")
	errSharedSecretCompute   = errors.New("failed to compute ECDH shared secret")
)

const (
	decimalBase             = 10
	identityPartCount       = 2
	p256ScalarSize          = 32
	p256PointPrefix         = 0x04
	p256UncompressedKeySize = 65
	rsaChallengeBlockSize   = 64
	maxRSAChallengeLevel    = 1000000
	packetTypeMask          = 0x0F
	generationIDShift       = 32
	fromServerShift         = 40
	fakeSignatureSize       = 8
	ivAlphaSize             = 10
	sha1NumBufSize          = 20
	bitsPerByte             = 8
)

type Identity struct {
	PrivateKey *ecdsa.PrivateKey
	Offset     uint64
}

func (id *Identity) PublicKeyBase64() string {
	pubBytes, err := id.PrivateKey.PublicKey.Bytes()
	if err != nil {
		return ""
	}
	if len(pubBytes) != p256UncompressedKeySize || pubBytes[0] != p256PointPrefix {
		return ""
	}

	x := new(big.Int).SetBytes(pubBytes[1 : 1+p256ScalarSize])
	y := new(big.Int).SetBytes(pubBytes[1+p256ScalarSize : p256UncompressedKeySize])

	data := struct {
		BitInfo asn1.BitString
		Size    int
		X       *big.Int
		Y       *big.Int
	}{
		BitInfo: asn1.BitString{Bytes: []byte{0x00}, BitLength: 1},
		Size:    p256ScalarSize,
		X:       x,
		Y:       y,
	}
	bytes, _ := asn1.Marshal(data)

	return base64.StdEncoding.EncodeToString(bytes)
}

func (id *Identity) String() string {
	d, err := id.PrivateKey.Bytes()
	if err != nil {
		// Keep String side-effect free; invalid key should not crash callers.
		return fmt.Sprintf(":%d", id.Offset)
	}

	return fmt.Sprintf("%s:%d", base64.StdEncoding.EncodeToString(d), id.Offset)
}

func IdentityFromString(s string) (*Identity, error) {
	parts := strings.Split(s, ":")
	if len(parts) != identityPartCount {
		return nil, errInvalidIdentityFormat
	}
	dBytes, err := base64.StdEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, err
	}
	offset, err := strconv.ParseUint(parts[1], decimalBase, 64)
	if err != nil {
		return nil, err
	}
	priv, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), dBytes)
	if err != nil {
		// Backward compatibility: historical identity strings might store
		// non-padded scalars; normalize to SEC 1 fixed-size raw key.
		if len(dBytes) >= p256ScalarSize {
			return nil, err
		}
		padded := make([]byte, p256ScalarSize)
		copy(padded[p256ScalarSize-len(dBytes):], dBytes)
		priv, err = ecdsa.ParseRawPrivateKey(elliptic.P256(), padded)
		if err != nil {
			return nil, err
		}
	}

	return &Identity{PrivateKey: priv, Offset: offset}, nil
}

func GetUidFromPublicKey(publicKey string) string {
	sum := sha1.Sum([]byte(publicKey))

	return base64.StdEncoding.EncodeToString(sum[:])
}

type Crypt struct {
	Identity           *Identity
	CachedKeys         map[uint64]KeyNonce
	IvStruct           []byte
	FakeSignature      []byte
	AlphaTmp           []byte
	keyMu              sync.Mutex
	CryptoInitComplete bool
}

type KeyNonce struct {
	Key   []byte
	Nonce []byte
	Gen   uint32
}

// makeCacheKey packs (fromServer, packetType, generationID) into a map key without allocating.
func makeCacheKey(fromServer bool, packetType byte, generationID uint32) uint64 {
	var key uint64
	if fromServer {
		key = 1 << fromServerShift
	}
	key |= uint64(packetType&packetTypeMask) << generationIDShift
	key |= uint64(generationID)

	return key
}

func NewCrypt(id *Identity) *Crypt {
	return &Crypt{
		Identity:      id,
		FakeSignature: make([]byte, fakeSignatureSize),
		CachedKeys:    make(map[uint64]KeyNonce),
	}
}

func (tc *Crypt) SolveRsaChallenge(data []byte, offset int, level int) ([]byte, error) {
	if level < 0 || level > maxRSAChallengeLevel {
		return nil, errRSAChallengeOutRange
	}
	x := new(big.Int).SetBytes(data[offset : offset+rsaChallengeBlockSize])
	n := new(big.Int).SetBytes(data[offset+rsaChallengeBlockSize : offset+2*rsaChallengeBlockSize])

	// y = x^(2^level) mod n via repeated squaring.
	y := new(big.Int).Set(x)
	for range level {
		y.Mul(y, y)
		y.Mod(y, n)
	}

	res := y.Bytes()
	if len(res) < rsaChallengeBlockSize {
		aligned := make([]byte, rsaChallengeBlockSize)
		copy(aligned[rsaChallengeBlockSize-len(res):], res)
		res = aligned
	} else if len(res) > rsaChallengeBlockSize {
		res = res[len(res)-rsaChallengeBlockSize:]
	}

	return res, nil
}

func (tc *Crypt) InitCrypto(alpha, beta, omega string) error {
	alphaBytes, err := base64.StdEncoding.DecodeString(alpha)
	if err != nil {
		return fmt.Errorf("invalid alpha: %w", err)
	}
	betaBytes, err := base64.StdEncoding.DecodeString(beta)
	if err != nil {
		return fmt.Errorf("invalid beta: %w", err)
	}
	omegaBytes, err := base64.StdEncoding.DecodeString(omega)
	if err != nil {
		return fmt.Errorf("invalid omega: %w", err)
	}
	serverPubKey, err := ImportPublicKey(omegaBytes)
	if err != nil {
		return err
	}
	sharedSecret := tc.getSharedSecret(serverPubKey)
	if len(sharedSecret) == 0 {
		return errSharedSecretCompute
	}

	return tc.SetSharedSecret(alphaBytes, betaBytes, sharedSecret)
}

func (tc *Crypt) SetSharedSecret(alpha, beta, sharedKey []byte) error {
	tc.IvStruct = make([]byte, ivAlphaSize+len(beta))
	for i := range alpha {
		tc.IvStruct[i] = sharedKey[i] ^ alpha[i]
	}
	for i := range beta {
		tc.IvStruct[ivAlphaSize+i] = sharedKey[ivAlphaSize+i] ^ beta[i]
	}

	h := sha1.New()
	h.Write(tc.IvStruct)
	copy(tc.FakeSignature, h.Sum(nil)[:fakeSignatureSize])

	tc.CryptoInitComplete = true

	return nil
}

func (tc *Crypt) DebugCryptoState() (int, string) {
	if len(tc.IvStruct) == 0 {
		return 0, ""
	}

	return len(tc.IvStruct), hex.EncodeToString(tc.FakeSignature)
}

func (tc *Crypt) getSharedSecret(pub *ecdsa.PublicKey) []byte {
	privECDH, err := tc.Identity.PrivateKey.ECDH()
	if err != nil {
		return nil
	}
	pubECDH, err := pub.ECDH()
	if err != nil {
		return nil
	}
	keyArr, err := privECDH.ECDH(pubECDH)
	if err != nil {
		return nil
	}
	if len(keyArr) > p256ScalarSize {
		keyArr = keyArr[len(keyArr)-p256ScalarSize:]
	} else if len(keyArr) < p256ScalarSize {
		aligned := make([]byte, p256ScalarSize)
		copy(aligned[p256ScalarSize-len(keyArr):], keyArr)
		keyArr = aligned
	}
	h := sha1.New()
	h.Write(keyArr)

	return h.Sum(nil)
}

func Hash512(data []byte) []byte {
	sum := sha512.Sum512(data)

	return sum[:]
}

func ImportPublicKey(data []byte) (*ecdsa.PublicKey, error) {
	// Canonical format (TS5/TS6): {BitString, Size, X, Y}
	var canonical struct {
		BitInfo asn1.BitString
		Size    int
		X       *big.Int
		Y       *big.Int
	}
	_, canonicalErr := asn1.Unmarshal(data, &canonical)
	if canonicalErr == nil {
		encoded, err := encodeUncompressedP256Point(canonical.X, canonical.Y)
		if err != nil {
			return nil, err
		}

		return ecdsa.ParseUncompressedPublicKey(elliptic.P256(), encoded)
	}

	// Legacy format (TeamSpeak): {X, Y, BitString, Size}
	var legacy struct {
		X       *big.Int
		Y       *big.Int
		BitInfo asn1.BitString
		Size    int
	}
	_, err := asn1.Unmarshal(data, &legacy)
	if err != nil {
		return nil, err
	}

	encoded, err := encodeUncompressedP256Point(legacy.X, legacy.Y)
	if err != nil {
		return nil, err
	}

	return ecdsa.ParseUncompressedPublicKey(elliptic.P256(), encoded)
}

func encodeUncompressedP256Point(x, y *big.Int) ([]byte, error) {
	if x == nil || y == nil {
		return nil, errInvalidPublicPoint
	}
	xBytes := x.Bytes()
	yBytes := y.Bytes()
	const fieldSize = 32
	if len(xBytes) > fieldSize || len(yBytes) > fieldSize {
		return nil, errInvalidPublicPoint
	}

	point := make([]byte, 1+fieldSize+fieldSize)
	point[0] = p256PointPrefix
	copy(point[1+fieldSize-len(xBytes):1+fieldSize], xBytes)
	copy(point[1+2*fieldSize-len(yBytes):], yBytes)

	return point, nil
}

func (id *Identity) SecurityLevel() int {
	h := sha1.New()
	h.Write([]byte(id.PublicKeyBase64()))
	var numBuf [sha1NumBufSize]byte
	h.Write(strconv.AppendUint(numBuf[:0], id.Offset, decimalBase))

	return countLeadingZeros(h.Sum(nil))
}

// UpgradeToLevel increments Offset until SecurityLevel reaches targetLevel.
func (id *Identity) UpgradeToLevel(targetLevel int, ctx context.Context) error {
	prefix := []byte(id.PublicKeyBase64())
	h := sha1.New()
	var numBuf [sha1NumBufSize]byte
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			h.Reset()
			h.Write(prefix)
			h.Write(strconv.AppendUint(numBuf[:0], id.Offset, decimalBase))
			if countLeadingZeros(h.Sum(nil)) >= targetLevel {
				return nil
			}
			id.Offset++
		}
	}
}

func GenerateIdentity(targetLevel int) (*Identity, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	id := &Identity{PrivateKey: priv}

	prefix := []byte(id.PublicKeyBase64())
	h := sha1.New()
	var numBuf [sha1NumBufSize]byte
	for {
		h.Reset()
		h.Write(prefix)
		h.Write(strconv.AppendUint(numBuf[:0], id.Offset, decimalBase))
		if countLeadingZeros(h.Sum(nil)) >= targetLevel {
			return id, nil
		}
		id.Offset++
	}
}

func countLeadingZeros(data []byte) int {
	zeros := 0
	for _, b := range data {
		if b == 0 {
			zeros += bitsPerByte
		} else {
			// Security level counts trailing zero bits in SHA1(prefix||offset), LSB-first.
			for i := range bitsPerByte {
				if (b & (1 << uint(i))) == 0 {
					zeros++
				} else {
					return zeros
				}
			}
		}
	}

	return zeros
}
