package transport_test

import (
	"testing"
	"time"

	"github.com/honeybbq/teamspeak-go/transport"
)

func TestPacketTypeExtraction(t *testing.T) {
	tests := []struct {
		typeFlagged byte
		wantType    transport.PacketType
	}{
		{0x00, transport.PacketTypeVoice},
		{0x01, transport.PacketTypeVoiceWhisper},
		{0x02, transport.PacketTypeCommand},
		{0x03, transport.PacketTypeCommandLow},
		{0x04, transport.PacketTypePing},
		{0x05, transport.PacketTypePong},
		{0x06, transport.PacketTypeAck},
		{0x07, transport.PacketTypeAckLow},
		{0x08, transport.PacketTypeInit1},
		{0x82, transport.PacketTypeCommand}, // Unencrypted | Command
		{0xE2, transport.PacketTypeCommand}, // all flags | Command
		{0x88, transport.PacketTypeInit1},   // Unencrypted | Init1
	}
	for _, tt := range tests {
		p := &transport.Packet{TypeFlagged: tt.typeFlagged}
		if p.Type() != tt.wantType {
			t.Errorf("TypeFlagged=0x%02X: Type()=%d, want %d", tt.typeFlagged, p.Type(), tt.wantType)
		}
	}
}

func TestPacketFlagsExtraction(t *testing.T) {
	tests := []struct {
		typeFlagged byte
		wantFlags   transport.PacketFlags
	}{
		{0x10, transport.PacketFlagFragmented},
		{0x20, transport.PacketFlagNewProtocol},
		{0x40, transport.PacketFlagCompressed},
		{0x80, transport.PacketFlagUnencrypted},
		{
			0xF0,
			transport.PacketFlagFragmented | transport.PacketFlagNewProtocol |
				transport.PacketFlagCompressed | transport.PacketFlagUnencrypted,
		},
		{0x02, 0},
	}
	for _, tt := range tests {
		p := &transport.Packet{TypeFlagged: tt.typeFlagged}
		if p.Flags() != tt.wantFlags {
			t.Errorf("TypeFlagged=0x%02X: Flags()=0x%02X, want 0x%02X", tt.typeFlagged, p.Flags(), tt.wantFlags)
		}
	}
}

func TestPacketIsUnencrypted(t *testing.T) {
	tests := []struct {
		typeFlagged byte
		want        bool
	}{
		{byte(transport.PacketFlagUnencrypted) | byte(transport.PacketTypeCommand), true},
		{byte(transport.PacketTypeCommand), false},
		{byte(transport.PacketFlagCompressed) | byte(transport.PacketTypeCommand), false},
		{0xFF, true},
	}
	for _, tt := range tests {
		p := &transport.Packet{TypeFlagged: tt.typeFlagged}
		if p.IsUnencrypted() != tt.want {
			t.Errorf("TypeFlagged=0x%02X: IsUnencrypted()=%v, want %v", tt.typeFlagged, p.IsUnencrypted(), tt.want)
		}
	}
}

func TestBuildParseC2SHeaderRoundtrip(t *testing.T) {
	tests := []struct {
		id          uint16
		clientID    uint16
		typeFlagged byte
	}{
		{0x0001, 0x0001, byte(transport.PacketTypeCommand)},
		{0xFFFF, 0xFFFF, byte(transport.PacketTypeInit1) | byte(transport.PacketFlagUnencrypted)},
		{0x1234, 0x5678, byte(transport.PacketTypeVoice) | byte(transport.PacketFlagUnencrypted)},
		{0x0000, 0x0000, 0x00},
	}
	for _, tt := range tests {
		p := &transport.Packet{ID: tt.id, ClientID: tt.clientID, TypeFlagged: tt.typeFlagged}
		header := p.BuildC2SHeader()
		if len(header) != 5 {
			t.Fatalf("C2S header len=%d, want 5", len(header))
		}
		p2 := &transport.Packet{}
		p2.ParseC2SHeader(header)
		if p2.ID != p.ID {
			t.Errorf("ID: got %d, want %d", p2.ID, p.ID)
		}
		if p2.ClientID != p.ClientID {
			t.Errorf("ClientID: got %d, want %d", p2.ClientID, p.ClientID)
		}
		if p2.TypeFlagged != p.TypeFlagged {
			t.Errorf("TypeFlagged: got 0x%02X, want 0x%02X", p2.TypeFlagged, p.TypeFlagged)
		}
	}
}

func TestParseS2CHeader(t *testing.T) {
	tests := []struct {
		raw        []byte
		wantID     uint16
		wantTypeFl byte
	}{
		{[]byte{0x00, 0x01, byte(transport.PacketTypeCommand)}, 1, byte(transport.PacketTypeCommand)},
		{[]byte{0xFF, 0xFF, byte(transport.PacketTypeInit1)}, 0xFFFF, byte(transport.PacketTypeInit1)},
		{[]byte{0x12, 0x34, 0x82}, 0x1234, 0x82},
	}
	for _, tt := range tests {
		p := &transport.Packet{}
		p.ParseS2CHeader(tt.raw)
		if p.ID != tt.wantID {
			t.Errorf("S2C ID: got %d, want %d", p.ID, tt.wantID)
		}
		if p.TypeFlagged != tt.wantTypeFl {
			t.Errorf("S2C TypeFlagged: got 0x%02X, want 0x%02X", p.TypeFlagged, tt.wantTypeFl)
		}
	}
}

func TestPacketTypeConstants(t *testing.T) {
	if transport.PacketTypeVoice != 0 {
		t.Error("PacketTypeVoice should be 0")
	}
	if transport.PacketTypeVoiceWhisper != 1 {
		t.Error("PacketTypeVoiceWhisper should be 1")
	}
	if transport.PacketTypeCommand != 2 {
		t.Error("PacketTypeCommand should be 2")
	}
	if transport.PacketTypeCommandLow != 3 {
		t.Error("PacketTypeCommandLow should be 3")
	}
	if transport.PacketTypePing != 4 {
		t.Error("PacketTypePing should be 4")
	}
	if transport.PacketTypePong != 5 {
		t.Error("PacketTypePong should be 5")
	}
	if transport.PacketTypeAck != 6 {
		t.Error("PacketTypeAck should be 6")
	}
	if transport.PacketTypeAckLow != 7 {
		t.Error("PacketTypeAckLow should be 7")
	}
	if transport.PacketTypeInit1 != 8 {
		t.Error("PacketTypeInit1 should be 8")
	}
}

func TestPacketReceivedAt(t *testing.T) {
	now := time.Now()
	p := &transport.Packet{ReceivedAt: now}
	if !p.ReceivedAt.Equal(now) {
		t.Error("ReceivedAt mismatch")
	}
}

func TestPacketDataField(t *testing.T) {
	data := []byte{0x01, 0x02, 0x03}
	p := &transport.Packet{Data: data}
	if len(p.Data) != 3 {
		t.Errorf("Data len=%d, want 3", len(p.Data))
	}
}
