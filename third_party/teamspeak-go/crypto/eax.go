package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	cryptosubtle "crypto/subtle"
	"errors"
	"sync"

	"github.com/tink-crypto/tink-go/v2/mac/subtle"
)

const (
	eaxTagByte0       = 0
	eaxTagByte1       = 1
	eaxTagByte2       = 2
	eaxTagSize        = 8
	eaxBlockSize      = 16
	eaxPoolBufferSize = 528
)

// cmacInputPool sizes buffers for nonce + header + ciphertext (TS3 wire limits).
var cmacInputPool = sync.Pool{
	New: func() any {
		buf := make([]byte, eaxPoolBufferSize)

		return &buf
	},
}

// EAX implementation for TeamSpeak 3 (64-bit tag).
type EAX struct {
	block      cipher.Block
	cmacHasher *subtle.AESCMAC
}

func NewEAX(key []byte) (*EAX, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	cmacHasher, err := subtle.NewAESCMAC(key, eaxBlockSize)
	if err != nil {
		return nil, err
	}

	return &EAX{block: block, cmacHasher: cmacHasher}, nil
}

var ErrEAXTagMismatch = errors.New("EAX tag mismatch")

func (e *EAX) Encrypt(nonce, header, plaintext []byte) ([]byte, []byte, error) {
	nStar, _ := e.cmac(eaxTagByte0, nonce)
	hStar, _ := e.cmac(eaxTagByte1, header)

	// CTR encryption
	stream := cipher.NewCTR(e.block, nStar)
	ciphertext := make([]byte, len(plaintext))
	stream.XORKeyStream(ciphertext, plaintext)

	cStar, _ := e.cmac(eaxTagByte2, ciphertext)

	tag := make([]byte, eaxTagSize)
	for i := range eaxTagSize {
		tag[i] = nStar[i] ^ hStar[i] ^ cStar[i]
	}

	return ciphertext, tag, nil
}

func (e *EAX) Decrypt(nonce, header, ciphertext, tag []byte) ([]byte, error) {
	nStar, _ := e.cmac(eaxTagByte0, nonce)
	hStar, _ := e.cmac(eaxTagByte1, header)
	cStar, _ := e.cmac(eaxTagByte2, ciphertext)

	var expected [eaxTagSize]byte
	for i := range eaxTagSize {
		expected[i] = nStar[i] ^ hStar[i] ^ cStar[i]
	}
	if cryptosubtle.ConstantTimeCompare(expected[:], tag[:eaxTagSize]) != 1 {
		return nil, ErrEAXTagMismatch
	}

	// CTR decryption (same as encryption)
	stream := cipher.NewCTR(e.block, nStar)
	plaintext := make([]byte, len(ciphertext))
	stream.XORKeyStream(plaintext, ciphertext)

	return plaintext, nil
}

func (e *EAX) cmac(tag byte, data []byte) ([]byte, error) {
	inputLen := eaxBlockSize + len(data)

	inputBufPtr, ok := cmacInputPool.Get().(*[]byte)
	if !ok || inputBufPtr == nil {
		buf := make([]byte, inputLen)
		inputBufPtr = &buf
	}
	inputBuf := *inputBufPtr
	if cap(inputBuf) < inputLen {
		inputBuf = make([]byte, inputLen)
	} else {
		inputBuf = inputBuf[:inputLen]
	}

	for i := range eaxBlockSize - 1 {
		inputBuf[i] = 0
	}
	inputBuf[eaxBlockSize-1] = tag
	copy(inputBuf[eaxBlockSize:], data)

	result, err := e.cmacHasher.ComputeMAC(inputBuf)

	cmacInputPool.Put(&inputBuf)

	return result, err
}
