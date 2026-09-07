package transport

import (
	"encoding/binary"
	"time"
)

type PacketType byte

const (
	PacketTypeVoice        PacketType = 0
	PacketTypeVoiceWhisper PacketType = 1
	PacketTypeCommand      PacketType = 2
	PacketTypeCommandLow   PacketType = 3
	PacketTypePing         PacketType = 4
	PacketTypePong         PacketType = 5
	PacketTypeAck          PacketType = 6
	PacketTypeAckLow       PacketType = 7
	PacketTypeInit1        PacketType = 8
)

type PacketFlags byte

const (
	PacketFlagFragmented  PacketFlags = 0x10
	PacketFlagNewProtocol PacketFlags = 0x20
	PacketFlagCompressed  PacketFlags = 0x40
	PacketFlagUnencrypted PacketFlags = 0x80
)

type Packet struct {
	ReceivedAt   time.Time
	Data         []byte
	GenerationID uint32
	ID           uint16
	ClientID     uint16
	TypeFlagged  byte
}

func (p *Packet) Type() PacketType {
	return PacketType(p.TypeFlagged & 0x0F)
}

func (p *Packet) Flags() PacketFlags {
	return PacketFlags(p.TypeFlagged & 0xF0)
}

func (p *Packet) IsUnencrypted() bool {
	return (p.Flags() & PacketFlagUnencrypted) != 0
}

func (p *Packet) BuildC2SHeader() []byte {
	header := make([]byte, 5)
	binary.BigEndian.PutUint16(header[0:2], p.ID)
	binary.BigEndian.PutUint16(header[2:4], p.ClientID)
	header[4] = p.TypeFlagged

	return header
}

func (p *Packet) ParseS2CHeader(raw []byte) {
	p.ID = binary.BigEndian.Uint16(raw[0:2])
	p.TypeFlagged = raw[2]
}

func (p *Packet) ParseC2SHeader(raw []byte) {
	p.ID = binary.BigEndian.Uint16(raw[0:2])
	p.ClientID = binary.BigEndian.Uint16(raw[2:4])
	p.TypeFlagged = raw[4]
}
