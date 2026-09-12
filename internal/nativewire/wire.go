// Package nativewire defines the versioned Resona protocol, independent of GUI and audio devices.
package nativewire

import (
	"encoding/binary"
	"errors"
	"io"

	"github.com/fxamacker/cbor/v2"
)

const (
	ALPN                 = "resona-exp-1"
	DefaultPort          = "9988"
	MaxFrame             = 65536
	MaxVoicePayload      = 1024
	MaxMembers           = 64
	MaxChannels          = 64
	ClientVoiceHeader    = 7
	ServerVoiceHeader    = 13
	AuthenticationFailed = 2
)
const (
	HelloKind uint8 = iota + 1
	WelcomeKind
	StateKind
	MoveKind
	ChatKind
	VoiceStateKind
	ReplyKind
	MessageKind
)

var ErrPacket = errors.New("invalid Resona packet")

type Frame struct {
	_       struct{} `cbor:",toarray"`
	Kind    uint8
	Request uint32
	Body    cbor.RawMessage
}
type Hello struct {
	_        struct{} `cbor:",toarray"`
	Nickname string
	Password string
}
type Channel struct {
	_           struct{} `cbor:",toarray"`
	ID          uint16
	Name        string
	Description string
}
type Member struct {
	_        struct{} `cbor:",toarray"`
	ID       uint16
	Channel  uint16
	Nickname string
	Instance string
	Muted    bool
	Deafened bool
	Epoch    uint32
}
type State struct {
	_        struct{} `cbor:",toarray"`
	Name     string
	Self     uint16
	Epoch    uint32
	Channels []Channel
	Members  []Member
}
type Command struct {
	_        struct{} `cbor:",toarray"`
	Channel  uint16
	Text     string
	Muted    bool
	Deafened bool
}
type Reply struct {
	_    struct{} `cbor:",toarray"`
	Code uint8
}

const (
	OK uint8 = iota
	Rejected
	WrongChannel
	RateLimited
)

type Message struct {
	_        struct{} `cbor:",toarray"`
	Channel  uint16
	Sender   uint16
	Nickname string
	Text     string
}

var enc = func() cbor.EncMode {
	m, err := cbor.CanonicalEncOptions().EncMode()
	if err != nil {
		panic(err)
	}
	return m
}()
var dec = func() cbor.DecMode {
	m, err := (cbor.DecOptions{MaxNestedLevels: 8, MaxArrayElements: 256, MaxMapPairs: 16, IndefLength: cbor.IndefLengthForbidden, TagsMd: cbor.TagsForbidden, DupMapKey: cbor.DupMapKeyEnforcedAPF}).DecMode()
	if err != nil {
		panic(err)
	}
	return m
}()

func Pack(kind uint8, request uint32, body any) ([]byte, error) {
	payload, err := enc.Marshal(body)
	if err != nil {
		return nil, err
	}
	data, err := enc.Marshal(Frame{Kind: kind, Request: request, Body: payload})
	if err != nil {
		return nil, err
	}
	if len(data) > MaxFrame {
		return nil, ErrPacket
	}
	result := make([]byte, 4+len(data))
	binary.BigEndian.PutUint32(result, uint32(len(data)))
	copy(result[4:], data)
	return result, nil
}
func Write(w io.Writer, kind uint8, request uint32, body any) error {
	data, err := Pack(kind, request, body)
	if err != nil {
		return err
	}
	for len(data) > 0 {
		n, e := w.Write(data)
		if e != nil {
			return e
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}
func Read(r io.Reader) (Frame, error) {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return Frame{}, err
	}
	size := binary.BigEndian.Uint32(header[:])
	if size == 0 || size > MaxFrame {
		return Frame{}, ErrPacket
	}
	data := make([]byte, size)
	if _, err := io.ReadFull(r, data); err != nil {
		return Frame{}, err
	}
	var f Frame
	err := dec.Unmarshal(data, &f)
	return f, err
}
func Decode(f Frame, value any) error { return dec.Unmarshal(f.Body, value) }

// Voice uses one Opus frame per datagram. Epoch belongs to the sender on upload
// and to the recipient on download; neither channel IDs nor claimed sender IDs are accepted on upload.
type Voice struct {
	Epoch, SenderEpoch uint32
	Sender, Sequence   uint16
	End                bool
	Data               []byte
}

func EncodeVoice(v Voice, downstream bool) ([]byte, error) {
	if v.Epoch == 0 || len(v.Data) > MaxVoicePayload || (v.End != (len(v.Data) == 0)) {
		return nil, ErrPacket
	}
	header := ClientVoiceHeader
	if downstream {
		header = ServerVoiceHeader
		if v.Sender == 0 {
			return nil, ErrPacket
		}
	}
	b := make([]byte, header+len(v.Data))
	if v.End {
		b[0] = 1
	}
	binary.BigEndian.PutUint32(b[1:5], v.Epoch)
	binary.BigEndian.PutUint16(b[5:7], v.Sequence)
	if downstream {
		if v.SenderEpoch == 0 {
			return nil, ErrPacket
		}
		binary.BigEndian.PutUint16(b[7:9], v.Sender)
		binary.BigEndian.PutUint32(b[9:13], v.SenderEpoch)
	}
	copy(b[header:], v.Data)
	return b, nil
}
func DecodeVoice(b []byte, downstream bool) (Voice, error) {
	header := ClientVoiceHeader
	if downstream {
		header = ServerVoiceHeader
	}
	if len(b) < header || len(b) > header+MaxVoicePayload || b[0] > 1 {
		return Voice{}, ErrPacket
	}
	v := Voice{Epoch: binary.BigEndian.Uint32(b[1:5]), Sequence: binary.BigEndian.Uint16(b[5:7]), End: b[0] == 1, Data: b[header:]}
	if downstream {
		v.Sender = binary.BigEndian.Uint16(b[7:9])
		v.SenderEpoch = binary.BigEndian.Uint32(b[9:13])
		if v.Sender == 0 || v.SenderEpoch == 0 {
			return Voice{}, ErrPacket
		}
	}
	if v.Epoch == 0 || v.End != (len(v.Data) == 0) {
		return Voice{}, ErrPacket
	}
	return v, nil
}
