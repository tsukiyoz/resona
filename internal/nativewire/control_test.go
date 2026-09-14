package nativewire

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"reflect"
	"testing"

	"github.com/tsukiyoz/resona/internal/nativewire/pb"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

func TestControlProtobufGoldenAndUnknownFields(t *testing.T) {
	packet, err := Pack(HelloKind, 0, Hello{Nickname: "tester", Password: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(packet) != "0000001408011a100a067465737465721206736563726574" {
		t.Fatalf("wire changed: %x", packet)
	}
	var frame pb.Frame
	if err := proto.Unmarshal(packet[4:], &frame); err != nil {
		t.Fatal(err)
	}
	frame.Body = protowire.AppendTag(frame.Body, 100, protowire.BytesType)
	frame.Body = protowire.AppendString(frame.Body, "future")
	data, _ := proto.Marshal(&frame)
	data = protowire.AppendTag(data, 100, protowire.VarintType)
	data = protowire.AppendVarint(data, 42)
	wire := make([]byte, 4)
	binary.BigEndian.PutUint32(wire, uint32(len(data)))
	f, err := Read(bytes.NewReader(append(wire, data...)))
	var got Hello
	if err != nil || Decode(f, &got) != nil || got != (Hello{Nickname: "tester", Password: "secret"}) {
		t.Fatalf("unknown field compatibility: %+v %v", got, err)
	}
}

func TestControlBoundsAndTypeSafety(t *testing.T) {
	cases := []struct {
		kind    uint8
		message proto.Message
		dest    any
	}{
		{MoveKind, &pb.Command{Channel: 65536}, &Command{}},
		{ReplyKind, &pb.Reply{Code: 256}, &Reply{}},
		{MessageKind, &pb.Message{Sender: 65536}, &Message{}},
		{StateKind, &pb.State{Self: 65536}, &State{}},
		{StateKind, &pb.State{Channels: []*pb.Channel{{Id: 65536}}}, &State{}},
		{StateKind, &pb.State{Members: []*pb.Member{{Channel: 65536}}}, &State{}},
		{StateKind, &pb.State{Members: make([]*pb.Member, MaxMembers+1)}, &State{}},
		{StateKind, &pb.State{Channels: make([]*pb.Channel, MaxChannels+1)}, &State{}},
	}
	for i, tc := range cases {
		b, err := proto.Marshal(tc.message)
		if err != nil {
			t.Fatal(err)
		}
		if Decode(Frame{Kind: tc.kind, Body: b}, tc.dest) == nil {
			t.Fatalf("case %d accepted invalid value", i)
		}
	}
	if _, err := Pack(HelloKind, 0, Command{}); err == nil {
		t.Fatal("kind/body mismatch accepted")
	}
	if Decode(Frame{Kind: HelloKind}, &Command{}) == nil {
		t.Fatal("decoded wrong type")
	}
	var nilHello *Hello
	if Decode(Frame{Kind: HelloKind}, nilHello) == nil {
		t.Fatal("nil destination accepted")
	}
	for _, kind := range []pb.Kind{0, -1, 256} {
		b, _ := proto.Marshal(&pb.Frame{Kind: kind})
		wire := make([]byte, 4)
		binary.BigEndian.PutUint32(wire, uint32(len(b)))
		if _, err := Read(bytes.NewReader(append(wire, b...))); err == nil {
			t.Fatalf("kind %d accepted", kind)
		}
	}
	if _, err := Pack(ChatKind, 1, Command{Text: string(bytes.Repeat([]byte{'x'}, MaxFrame))}); err == nil {
		t.Fatal("oversized encode accepted")
	}
	if _, err := Pack(HelloKind, 0, Hello{Nickname: string([]byte{0xff})}); err == nil {
		t.Fatal("invalid UTF-8 accepted")
	}
}

func TestControlEmptyDefaultsAndAtomicDecode(t *testing.T) {
	packet, err := Pack(ReplyKind, 1, Reply{Code: OK})
	if err != nil {
		t.Fatal(err)
	}
	f, err := Read(bytes.NewReader(packet))
	if err != nil {
		t.Fatal(err)
	}
	r := Reply{Code: Rejected}
	if err := Decode(f, &r); err != nil || r.Code != OK {
		t.Fatal("empty protobuf success body rejected")
	}
	cmd := Command{Muted: true, Deafened: true}
	if err := Decode(Frame{Kind: VoiceStateKind}, &cmd); err != nil || cmd != (Command{}) {
		t.Fatal("voice state defaults not applied")
	}
	s := State{Name: "unchanged", Self: 1, Channels: []Channel{{ID: 1}}}
	before := s
	bad, _ := proto.Marshal(&pb.State{Members: []*pb.Member{{Id: 65536}}})
	if Decode(Frame{Kind: StateKind, Body: bad}, &s) == nil || !reflect.DeepEqual(s, before) {
		t.Fatal("failed decode changed application state")
	}
}

func FuzzDecodeControl(f *testing.F) {
	f.Add(byte(StateKind), []byte{0x2a, 0})
	f.Add(byte(HelloKind), []byte{0x0a, 1, 'a'})
	f.Fuzz(func(t *testing.T, kind byte, data []byte) {
		frame := Frame{Kind: kind, Body: data}
		for _, v := range []any{&Hello{}, &Command{}, &Reply{}, &Message{}, &State{}} {
			_ = Decode(frame, v)
		}
	})
}
