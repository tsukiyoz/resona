package handshake

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"math"
	"time"

	"github.com/honeybbq/teamspeak-go/commands"
	"github.com/honeybbq/teamspeak-go/crypto"
)

const InitVersion = 1566914096 // 3.5.0 [Stable]
const (
	initVersionLen = 4
	initTypeLen    = 1
	initStepLen    = 21
)

// ProcessInit1 handles the TS3INIT1 handshake steps.
func ProcessInit1(tc *crypto.Crypt, data []byte) []byte {
	if data == nil || (len(data) >= 1 && data[0] == 0x7F) {
		return buildInit1StartPacket()
	}

	switch data[0] {
	case 0:
		return buildInit1Step1Packet(data)
	case 1:
		return buildInit1Step2Packet(data)
	case 2:
		return buildInit1Step3Packet(data)
	case 3:
		return buildInit1Step4Packet(tc, data)
	default:
		return nil
	}
}

func buildInit1StartPacket() []byte {
	sendData := make([]byte, initVersionLen+initTypeLen+4+4+8)
	binary.BigEndian.PutUint32(sendData[0:4], InitVersion)
	sendData[4] = 0x00
	nowUnix := time.Now().Unix()
	nowUnix = max(nowUnix, 0)
	nowUnix = min(nowUnix, int64(math.MaxUint32))
	binary.BigEndian.PutUint32(sendData[5:9], uint32(nowUnix))
	_, err := rand.Read(sendData[9:13])
	if err != nil {
		return nil
	}

	return sendData
}

func buildInit1Step1Packet(data []byte) []byte {
	if len(data) != initStepLen {
		return nil
	}
	sendData := make([]byte, initTypeLen+16+4)
	sendData[0] = 0x01
	tsRand := binary.LittleEndian.Uint32(data[initVersionLen+initTypeLen+4 : initVersionLen+initTypeLen+8])
	binary.BigEndian.PutUint32(sendData[initTypeLen+16:initTypeLen+16+4], tsRand)

	return sendData
}

func buildInit1Step2Packet(data []byte) []byte {
	if len(data) != initStepLen {
		return nil
	}
	sendData := make([]byte, initVersionLen+initTypeLen+16+4)
	binary.BigEndian.PutUint32(sendData[0:4], InitVersion)
	sendData[4] = 0x02
	copy(sendData[5:25], data[1:21])

	return sendData
}

func buildInit1Step3Packet(data []byte) []byte {
	if len(data) != initVersionLen+initTypeLen+16+4 {
		return nil
	}
	sendData := make([]byte, initTypeLen+64+64+4+100)
	sendData[0] = 0x03
	sendData[initTypeLen+64-1] = 1
	sendData[initTypeLen+64+64-1] = 1
	binary.BigEndian.PutUint32(sendData[initTypeLen+64+64:initTypeLen+64+64+4], 1)

	return sendData
}

func buildInit1Step4Packet(tc *crypto.Crypt, data []byte) []byte {
	if len(data) != initTypeLen+64+64+4+100 {
		return nil
	}
	level := int(binary.BigEndian.Uint32(data[1+128 : 1+128+4]))
	y, err := tc.SolveRsaChallenge(data, 1, level)
	if err != nil {
		return nil
	}

	tc.AlphaTmp = make([]byte, 10)
	_, err = rand.Read(tc.AlphaTmp)
	if err != nil {
		return nil
	}

	cmd := commands.BuildCommandOrdered("clientinitiv", [][2]string{
		{"alpha", base64.StdEncoding.EncodeToString(tc.AlphaTmp)},
		{"omega", tc.Identity.PublicKeyBase64()},
		{"ot", "1"},
		{"ip", ""},
	})
	cmdBytes := []byte(cmd)

	sendData := make([]byte, initVersionLen+initTypeLen+232+64+len(cmdBytes))
	binary.BigEndian.PutUint32(sendData[0:4], InitVersion)
	sendData[4] = 0x04
	copy(sendData[5:5+232], data[1:1+232])
	copy(sendData[5+232:5+232+64], y)
	copy(sendData[5+232+64:], cmdBytes)

	return sendData
}
