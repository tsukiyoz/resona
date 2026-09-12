package nativewire

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestFramesAndVoiceBounds(t *testing.T) {
	b, err := Pack(HelloKind, 0, Hello{Nickname: "tester", Password: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	f, err := Read(bytes.NewReader(b))
	var h Hello
	if err != nil || Decode(f, &h) != nil || h.Nickname != "tester" {
		t.Fatal("round trip failed")
	}
	for _, n := range []uint32{0, MaxFrame + 1, ^uint32(0)} {
		var h [4]byte
		binary.BigEndian.PutUint32(h[:], n)
		if _, err := Read(bytes.NewReader(h[:])); err == nil {
			t.Fatal("accepted oversized frame")
		}
	}
	for _, down := range []bool{false, true} {
		for _, end := range []bool{false, true} {
			v := Voice{Epoch: 7, SenderEpoch: 9, Sender: 3, Sequence: 65535, End: end}
			if !end {
				v.Data = bytes.Repeat([]byte{2}, MaxVoicePayload)
			}
			b, err := EncodeVoice(v, down)
			if err != nil {
				t.Fatal(err)
			}
			got, err := DecodeVoice(b, down)
			if err != nil || got.Epoch != v.Epoch || got.Sequence != v.Sequence || !bytes.Equal(got.Data, v.Data) {
				t.Fatal("voice round trip failed")
			}
			want := ClientVoiceHeader
			if down {
				want = ServerVoiceHeader
			}
			if len(b) != want+len(v.Data) {
				t.Fatal("unexpected header size")
			}
		}
	}
	if _, err := EncodeVoice(Voice{Epoch: 1, Data: make([]byte, MaxVoicePayload+1)}, false); err == nil {
		t.Fatal("accepted oversized audio")
	}
	for _, b := range [][]byte{nil, make([]byte, 6), make([]byte, 7), append([]byte{2}, make([]byte, 12)...)} {
		if _, err := DecodeVoice(b, false); err == nil {
			t.Fatal("accepted malformed voice")
		}
	}
}
func FuzzReadFrame(f *testing.F) {
	b, _ := Pack(HelloKind, 0, Hello{Nickname: "a"})
	f.Add(b)
	f.Fuzz(func(t *testing.T, b []byte) {
		_, _ = Read(bytes.NewReader(b))
		_, _ = DecodeVoice(b, false)
		_, _ = DecodeVoice(b, true)
	})
}
