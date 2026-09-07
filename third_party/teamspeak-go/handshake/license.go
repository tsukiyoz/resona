package handshake

import (
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"github.com/honeybbq/teamspeak-go/crypto"
	"github.com/oasisprotocol/curve25519-voi/curve"
	"github.com/oasisprotocol/curve25519-voi/curve/scalar"
)

var (
	errLicenseTooShort           = errors.New("license too short")
	errUnsupportedLicenseVersion = errors.New("unsupported license version")
	errInvalidLicenseTimes       = errors.New("license times are invalid")
	errIssuerStringNotTerminated = errors.New("non-null-terminated issuer string")
	errWrongKeyKindInLicense     = errors.New("wrong key kind in license")
	errInvalidLicenseBlockType   = errors.New("invalid license block type")
)

var licenseRootKey = []byte{
	0xcd, 0x0d, 0xe2, 0xae, 0xd4, 0x63, 0x45, 0x50, 0x9a, 0x7e, 0x3c, 0xfd, 0x8f, 0x68, 0xb3, 0xdc, 0x75, 0x55, 0xb2,
	0x9d, 0xcc, 0xec, 0x73, 0xcd, 0x18, 0x75, 0x0f, 0x99, 0x38, 0x12, 0x40, 0x8a,
}

type licenseBlockType byte

const (
	licenseBlockIntermediate licenseBlockType = 0
	licenseBlockServer       licenseBlockType = 2
	licenseBlockTs5Server    licenseBlockType = 8
	licenseBlockEphemeral    licenseBlockType = 32
)

type licenseBlock struct {
	key            []byte
	hash           []byte
	properties     [][]byte // TS5/TS6 server license properties
	issuer         string
	notValidBefore time.Time
	notValidAfter  time.Time
	blockType      licenseBlockType
	serverType     byte
}

type LicenseChain struct {
	Blocks []licenseBlock
}

type blockPayload struct {
	read       int
	issuer     string
	serverType byte
	properties [][]byte
}

func ParseLicenses(data []byte) (*LicenseChain, error) {
	if len(data) < 1 {
		return nil, errLicenseTooShort
	}
	if data[0] != 1 {
		return nil, errUnsupportedLicenseVersion
	}

	data = data[1:]
	res := &LicenseChain{}
	for len(data) > 0 {
		block, read, err := parseLicenseBlock(data)
		if err != nil {
			return nil, err
		}
		res.Blocks = append(res.Blocks, block)
		data = data[read:]
	}

	return res, nil
}

func (lc *LicenseChain) DeriveKey() ([]byte, error) {
	round := make([]byte, len(licenseRootKey))
	copy(round, licenseRootKey)
	for _, block := range lc.Blocks {
		next, err := block.deriveKey(round)
		if err != nil {
			return nil, err
		}
		round = next
	}

	return round, nil
}

func parseLicenseBlock(data []byte) (licenseBlock, int, error) {
	const minBlockLen = 42
	if len(data) < minBlockLen {
		return licenseBlock{}, 0, errLicenseTooShort
	}
	if data[0] != 0 {
		return licenseBlock{}, 0, fmt.Errorf("%w: %d", errWrongKeyKindInLicense, data[0])
	}

	blockType := licenseBlockType(data[33])
	payload, err := parseBlockPayload(blockType, data, minBlockLen)
	if err != nil {
		return licenseBlock{}, 0, err
	}

	notValidBefore := unixTimeStart.Add(time.Duration(binary.BigEndian.Uint32(data[34:38])+0x50e22700) * time.Second)
	notValidAfter := unixTimeStart.Add(time.Duration(binary.BigEndian.Uint32(data[38:42])+0x50e22700) * time.Second)
	if notValidAfter.Before(notValidBefore) {
		return licenseBlock{}, 0, errInvalidLicenseTimes
	}

	key := make([]byte, 32)
	copy(key, data[1:33])
	allLen := minBlockLen + payload.read
	hash := crypto.Hash512(data[1:allLen])
	block := licenseBlock{
		blockType:      blockType,
		issuer:         payload.issuer,
		notValidBefore: notValidBefore,
		notValidAfter:  notValidAfter,
		key:            key,
		hash:           hash[:32],
		serverType:     payload.serverType,
		properties:     payload.properties,
	}

	return block, allLen, nil
}

func parseBlockPayload(blockType licenseBlockType, data []byte, minBlockLen int) (blockPayload, error) {
	switch blockType {
	case licenseBlockIntermediate:
		return parseIntermediatePayload(data)
	case licenseBlockServer:
		return parseServerPayload(data)
	case licenseBlockTs5Server:
		return parseTs5ServerPayload(data, minBlockLen)
	case licenseBlockEphemeral:
		return blockPayload{}, nil
	default:
		return blockPayload{}, fmt.Errorf("%w: %d", errInvalidLicenseBlockType, blockType)
	}
}

func parseIntermediatePayload(data []byte) (blockPayload, error) {
	issuer, read, err := readNullString(data[46:])
	if err != nil {
		return blockPayload{}, err
	}

	return blockPayload{issuer: issuer, read: 5 + read}, nil
}

func parseServerPayload(data []byte) (blockPayload, error) {
	issuer, read, err := readNullString(data[47:])
	if err != nil {
		return blockPayload{}, err
	}

	return blockPayload{
		issuer:     issuer,
		read:       6 + read,
		serverType: data[42],
	}, nil
}

func parseTs5ServerPayload(data []byte, minBlockLen int) (blockPayload, error) {
	propertyCount := int(data[43])
	pos := 44
	properties := make([][]byte, 0, propertyCount)
	for range propertyCount {
		if pos >= len(data) {
			return blockPayload{}, errLicenseTooShort
		}
		propLen := int(data[pos])
		pos++
		if pos+propLen > len(data) {
			return blockPayload{}, errLicenseTooShort
		}
		prop := make([]byte, propLen)
		copy(prop, data[pos:pos+propLen])
		properties = append(properties, prop)
		pos += propLen
	}

	return blockPayload{
		read:       pos - minBlockLen,
		serverType: data[42],
		properties: properties,
	}, nil
}

func (lb *licenseBlock) deriveKey(parent []byte) ([]byte, error) {
	scalarBytes := make([]byte, 32)
	copy(scalarBytes, lb.hash)
	crypto.ClampScalar(scalarBytes)
	sc, err := scalar.NewFromBits(scalarBytes)
	if err != nil {
		return nil, err
	}

	pub := curve.NewEdwardsPoint()
	err = pub.UnmarshalBinary(lb.key)
	if err != nil {
		return nil, err
	}
	pub.Neg(pub)

	par := curve.NewEdwardsPoint()
	err = par.UnmarshalBinary(parent)
	if err != nil {
		return nil, err
	}
	par.Neg(par)

	res := curve.NewEdwardsPoint().Mul(pub, sc)
	res.Add(res, par)

	final, err := res.MarshalBinary()
	if err != nil {
		return nil, err
	}
	final[31] ^= 0x80

	return final, nil
}

func readNullString(data []byte) (string, int, error) {
	for i, b := range data {
		if b == 0 {
			return string(data[:i]), i, nil
		}
	}

	return "", 0, errIssuerStringNotTerminated
}

var unixTimeStart = time.Unix(0, 0)
