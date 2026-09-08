package teamspeak

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"

	"github.com/honeybbq/teamspeak-go/crypto"
	"github.com/honeybbq/teamspeak-go/transport"
)

func TestIncomingVoicePacketParsingAndObserverIsolation(t *testing.T) {
	id, err := crypto.GenerateIdentity(0)
	if err != nil {
		t.Fatal(err)
	}
	var packets []VoicePacket
	c := NewClient(id, "127.0.0.1:9987", "voice-test",
		WithVoiceObserver(func(packet VoicePacket) { packet.Data[0] = 99 }),
		WithVoiceObserver(func(packet VoicePacket) { packets = append(packets, packet) }))
	when := time.Now()
	c.handlePacket(&transport.Packet{ReceivedAt: when, TypeFlagged: byte(transport.PacketTypeVoice) | byte(transport.PacketFlagUnencrypted), Data: []byte{0x12, 0x34, 0x00, 0x2a, 0x04, 1, 2, 3}})
	if len(packets) != 1 {
		t.Fatalf("observers received %d packets", len(packets))
	}
	got := packets[0]
	if got.Sequence != 0x1234 || got.SenderID != 42 || got.Codec != 4 || got.Whisper || got.Encrypted || !got.ReceivedAt.Equal(when) || !bytes.Equal(got.Data, []byte{1, 2, 3}) {
		t.Fatalf("parsed packet = %+v", got)
	}

	c.handlePacket(&transport.Packet{TypeFlagged: byte(transport.PacketTypeVoiceWhisper), Data: []byte{0, 1, 0, 2, 5, 8}})
	if len(packets) != 2 || !packets[1].Whisper || !packets[1].Encrypted {
		t.Fatal("whisper packet was not identified")
	}
}

func TestIncomingVoiceRejectsMalformedAndOversizedPayloads(t *testing.T) {
	id, _ := crypto.GenerateIdentity(0)
	count := 0
	c := NewClient(id, "127.0.0.1:9987", "voice-test", WithVoiceObserver(func(VoicePacket) { count++ }))
	for _, data := range [][]byte{nil, {0}, {0, 1, 0, 2}, make([]byte, 5+maxVoiceDataBytes+1)} {
		c.handlePacket(&transport.Packet{TypeFlagged: byte(transport.PacketTypeVoice), Data: data})
	}
	if count != 0 {
		t.Fatalf("accepted %d malformed packets", count)
	}
}

func TestIncomingVoicePreservesEndAndOneByteOpusPackets(t *testing.T) {
	id, _ := crypto.GenerateIdentity(0)
	var packets []VoicePacket
	c := NewClient(id, "127.0.0.1:9987", "voice-test", WithVoiceObserver(func(packet VoicePacket) {
		packets = append(packets, packet)
	}))
	c.handlePacket(&transport.Packet{TypeFlagged: byte(transport.PacketTypeVoice), Data: voicePayload(7, 42, 4, nil)})
	c.handlePacket(&transport.Packet{TypeFlagged: byte(transport.PacketTypeVoice), Data: voicePayload(8, 42, 4, []byte{0})})
	if len(packets) != 2 {
		t.Fatalf("received %d packets", len(packets))
	}
	if !packets[0].End || len(packets[0].Data) != 0 || packets[0].Sequence != 7 || packets[0].SenderID != 42 {
		t.Fatalf("end packet = %+v", packets[0])
	}
	if packets[1].End || !bytes.Equal(packets[1].Data, []byte{0}) {
		t.Fatalf("one-byte Opus packet = %+v", packets[1])
	}
}

func TestInitialMuteOption(t *testing.T) {
	id, _ := crypto.GenerateIdentity(0)
	c := NewClient(id, "127.0.0.1:9987", "voice-test", WithInitialMute(true, true))
	if !c.clientInitOptions.inputMuted || !c.clientInitOptions.outputMuted {
		t.Fatal("initial mute option was not retained")
	}
}

func voicePayload(sequence, sender uint16, codec byte, data []byte) []byte {
	payload := make([]byte, 5+len(data))
	binary.BigEndian.PutUint16(payload[0:2], sequence)
	binary.BigEndian.PutUint16(payload[2:4], sender)
	payload[4] = codec
	copy(payload[5:], data)
	return payload
}
