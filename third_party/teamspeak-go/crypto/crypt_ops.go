package crypto

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"sync"
)

const (
	keySizeBytes      = 16
	init1PacketType   = 8
	hashInputMetaSize = 6
	clientSaltByte    = 0x31
	serverSaltByte    = 0x30
)

// keyPool reuses 16-byte AES key buffers for packet crypto.
var keyPool = sync.Pool{
	New: func() any {
		buf := make([]byte, keySizeBytes)

		return &buf
	},
}

// AcquireKeyBuffer returns a 16-byte buffer from keyPool or allocates one.
func AcquireKeyBuffer() []byte {
	bufPtr, ok := keyPool.Get().(*[]byte)
	if !ok || bufPtr == nil {
		return make([]byte, keySizeBytes)
	}

	return *bufPtr
}

// ReleaseKeyBuffer returns buf to keyPool only if len(buf) is the AES key size.
func ReleaseKeyBuffer(buf []byte) {
	if len(buf) == keySizeBytes {
		keyPool.Put(&buf)
	}
}

// Precomputed dummy key/nonce matching the TS3 client pre-crypto placeholder.
var (
	dummyKey   = []byte("c:\\windows\\syste")
	dummyNonce = []byte("m\\firewall32.cpl")
)

func (tc *Crypt) GetKeyNonce(
	fromServer bool,
	packetID uint16,
	generationID uint32,
	packetType byte,
	dummy bool,
) ([]byte, []byte) {
	if dummy {
		key := AcquireKeyBuffer()
		copy(key, dummyKey)

		return key, dummyNonce
	}

	cacheKey := makeCacheKey(fromServer, packetType, generationID)

	tc.keyMu.Lock()
	kn, ok := tc.CachedKeys[cacheKey]
	if !ok {
		tmpToHash := make([]byte, hashInputMetaSize+len(tc.IvStruct))
		if fromServer {
			tmpToHash[0] = serverSaltByte
		} else {
			tmpToHash[0] = clientSaltByte
		}
		tmpToHash[1] = packetType & packetTypeMask
		binary.BigEndian.PutUint32(tmpToHash[2:6], generationID)
		copy(tmpToHash[6:], tc.IvStruct)

		hash := sha256.Sum256(tmpToHash)
		kn = KeyNonce{
			Key:   append([]byte(nil), hash[0:keySizeBytes]...),
			Nonce: append([]byte(nil), hash[keySizeBytes:2*keySizeBytes]...),
			Gen:   generationID,
		}
		tc.CachedKeys[cacheKey] = kn
	}
	tc.keyMu.Unlock()

	key := AcquireKeyBuffer()
	copy(key, kn.Key)
	var packetIDBytes [2]byte
	binary.BigEndian.PutUint16(packetIDBytes[:], packetID)
	key[0] ^= packetIDBytes[0]
	key[1] ^= packetIDBytes[1]

	return key, kn.Nonce
}

var init1MAC = []byte("TS3INIT1")

var ErrFakeSignatureMismatch = errors.New("fake signature mismatch")

// Encrypt returns (ciphertext, MAC, err). Init1 and unencrypted packet types bypass EAX.
func (tc *Crypt) Encrypt(
	packetType byte,
	packetID uint16,
	generationID uint32,
	header, plaintext []byte,
	dummy bool,
	unencrypted bool,
) ([]byte, []byte, error) {
	if packetType == init1PacketType {
		return plaintext, init1MAC, nil
	}

	if unencrypted {
		return plaintext, tc.FakeSignature, nil
	}

	key, nonce := tc.GetKeyNonce(false, packetID, generationID, packetType, dummy)
	defer ReleaseKeyBuffer(key)

	eax, err := NewEAX(key)
	if err != nil {
		return nil, nil, err
	}
	ciphertext, mac, err := eax.Encrypt(nonce, header, plaintext)

	return ciphertext, mac, err
}

// Decrypt verifies and decrypts ciphertext; Init1 and unencrypted types pass through.
func (tc *Crypt) Decrypt(
	packetType byte,
	packetID uint16,
	generationID uint32,
	header, ciphertext, tag []byte,
	dummy bool,
	unencrypted bool,
) ([]byte, error) {
	if packetType == init1PacketType {
		return ciphertext, nil
	}

	if unencrypted {
		if subtle.ConstantTimeCompare(tag[:fakeSignatureSize], tc.FakeSignature) != 1 {
			return nil, ErrFakeSignatureMismatch
		}

		return ciphertext, nil
	}

	key, nonce := tc.GetKeyNonce(true, packetID, generationID, packetType, dummy)
	defer ReleaseKeyBuffer(key)

	eax, err := NewEAX(key)
	if err != nil {
		return nil, err
	}

	return eax.Decrypt(nonce, header, ciphertext, tag)
}
