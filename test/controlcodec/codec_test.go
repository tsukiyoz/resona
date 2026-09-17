package controlcodec

import (
	"bytes"
	"fmt"
	"reflect"
	"testing"

	w "github.com/tsukiyoz/resona/internal/nativewire"
	b "github.com/tsukiyoz/resona/test/controlcodec/baseline"
)

type fixture struct {
	name            string
	kind            uint8
	request         uint32
	value, old      any
	fresh, oldFresh func() any
}

func sample[T, B any](name string, kind uint8, request uint32, value T, old B) fixture {
	return fixture{name, kind, request, value, old, func() any { return new(T) }, func() any { return new(B) }}
}

func fixtures() []fixture {
	result := []fixture{
		sample("hello", w.HelloKind, 0, w.Hello{Nickname: "tester", Password: "secret"}, b.Hello{Nickname: "tester", Password: "secret"}),
		sample("move", w.MoveKind, 1, w.Command{Channel: 2}, b.Command{Channel: 2}),
		sample("voice_state", w.VoiceStateKind, 2, w.Command{Muted: true}, b.Command{Muted: true}),
		sample("reply_ok", w.ReplyKind, 2, w.Reply{}, b.Reply{}),
		sample("chat", w.MessageKind, 0, w.Message{Channel: 1, Sender: 2, Nickname: "tester", Text: "hello everyone"}, b.Message{Channel: 1, Sender: 2, Nickname: "tester", Text: "hello everyone"}),
	}
	for _, n := range []int{4, 64} {
		s := w.State{Name: "Resona", Self: 1, Epoch: 1}
		old := b.State{Name: s.Name, Self: s.Self, Epoch: s.Epoch}
		for i := uint16(1); i <= 4; i++ {
			name := fmt.Sprintf("Channel %d", i)
			s.Channels = append(s.Channels, w.Channel{ID: i, Name: name, Description: "Gaming voice channel"})
			old.Channels = append(old.Channels, b.Channel{ID: i, Name: name, Description: "Gaming voice channel"})
		}
		for i := 1; i <= n; i++ {
			m := w.Member{ID: uint16(i), Channel: uint16((i-1)%4 + 1), Nickname: fmt.Sprintf("Player%02d", i), Instance: fmt.Sprintf("instance-%024d", i), Muted: i%3 == 0, Epoch: 1}
			s.Members = append(s.Members, m)
			old.Members = append(old.Members, b.Member{ID: m.ID, Channel: m.Channel, Nickname: m.Nickname, Instance: m.Instance, Muted: m.Muted, Deafened: m.Deafened, Epoch: m.Epoch})
		}
		result = append(result, sample(fmt.Sprintf("state%d", n), w.StateKind, 0, s, old))
	}
	return result
}

func TestSizesAndRoundTrips(t *testing.T) {
	for _, s := range fixtures() {
		newWire, err := w.Pack(s.kind, s.request, s.value)
		if err != nil {
			t.Fatal(err)
		}
		oldWire, err := b.Pack(s.kind, s.request, s.old)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s CBOR=%d Protobuf=%d bytes (including framing)", s.name, len(oldWire), len(newWire))
		f, err := w.Read(bytes.NewReader(newWire))
		if err != nil {
			t.Fatal(err)
		}
		v := s.fresh()
		if err := w.Decode(f, v); err != nil || !reflect.DeepEqual(reflect.ValueOf(v).Elem().Interface(), s.value) {
			t.Fatalf("new roundtrip %s: %v", s.name, err)
		}
		of, err := b.Read(bytes.NewReader(oldWire))
		if err != nil {
			t.Fatal(err)
		}
		ov := s.oldFresh()
		if err := b.Decode(of, ov); err != nil || !reflect.DeepEqual(reflect.ValueOf(ov).Elem().Interface(), s.old) {
			t.Fatalf("old roundtrip %s: %v", s.name, err)
		}
	}
}

func BenchmarkControlCodec(bench *testing.B) {
	for _, s := range fixtures() {
		bench.Run(s.name, func(bench *testing.B) {
			for _, codec := range []string{"CBOR", "Protobuf"} {
				bench.Run(codec, func(bench *testing.B) {
					pack := w.Pack
					value, fresh := s.value, s.fresh
					decode := func(wire []byte, v any) error {
						f, e := w.Read(bytes.NewReader(wire))
						if e != nil {
							return e
						}
						return w.Decode(f, v)
					}
					if codec == "CBOR" {
						pack = b.Pack
						value, fresh = s.old, s.oldFresh
						decode = func(wire []byte, v any) error {
							f, e := b.Read(bytes.NewReader(wire))
							if e != nil {
								return e
							}
							return b.Decode(f, v)
						}
					}
					wire, err := pack(s.kind, s.request, value)
					if err != nil {
						bench.Fatal(err)
					}
					bench.Run("encode", func(bench *testing.B) {
						bench.ReportAllocs()
						for bench.Loop() {
							if _, e := pack(s.kind, s.request, value); e != nil {
								bench.Fatal(e)
							}
						}
					})
					bench.Run("decode", func(bench *testing.B) {
						bench.ReportAllocs()
						for bench.Loop() {
							if e := decode(wire, fresh()); e != nil {
								bench.Fatal(e)
							}
						}
					})
				})
			}
		})
	}
}
