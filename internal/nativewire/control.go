package nativewire

import (
	"math"

	"github.com/tsukiyoz/resona/internal/nativewire/pb"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

// Keep generated messages at the codec boundary: application snapshots remain
// ordinary copyable values, without protobuf runtime state or mutable pointers.
func marshalBody(kind uint8, value any) (proto.Message, error) {
	switch v := value.(type) {
	case WatchResources:
		if kind == WatchResourcesKind {
			return &pb.WatchResources{AllChannels: v.AllChannels, AllMembers: v.AllMembers}, nil
		}
	case CreateChannel:
		if kind == CreateChannelKind {
			return &pb.CreateChannel{Name: v.Name, Description: v.Description, Bitrate: v.Bitrate}, nil
		}
	case UpdateChannel:
		if kind == UpdateChannelKind {
			return &pb.UpdateChannel{Id: uint32(v.ID), Name: v.Name, Description: v.Description, Bitrate: v.Bitrate}, nil
		}
	case DeleteChannel:
		if kind == DeleteChannelKind {
			return &pb.DeleteChannel{Id: uint32(v.ID)}, nil
		}
	case Hello:
		if kind == HelloKind {
			return &pb.Hello{Nickname: v.Nickname, Password: v.Password, PublicKey: v.PublicKey, Signature: v.Signature}, nil
		}
	case ClaimOwner:
		if kind == ClaimOwnerKind {
			return &pb.ClaimOwner{Token: v.Token}, nil
		}
	case State:
		if (kind != WelcomeKind && kind != StateKind) || len(v.Channels) > MaxChannels || len(v.Members) > MaxMembers {
			return nil, ErrPacket
		}
		s := &pb.State{Name: v.Name, Self: uint32(v.Self), Epoch: v.Epoch, Channels: make([]*pb.Channel, len(v.Channels)), Members: make([]*pb.Member, len(v.Members)), IdentityUid: v.IdentityUID, ServerRole: v.ServerRole, CanClaimOwner: v.CanClaimOwner}
		s.CanManageChannels = v.CanManageChannels
		s.CanConfigureChannelAudio = v.CanConfigureChannelAudio
		s.CanWatchResources, s.Revision = v.CanWatchResources, v.Revision
		s.AllChannels, s.AllMembers, s.DefaultChannel = v.AllChannels, v.AllMembers, uint32(v.DefaultChannel)
		// One backing allocation per collection instead of one per member. These
		// fresh messages are initialized before publication and never copied later.
		channels := make([]pb.Channel, len(v.Channels))
		members := make([]pb.Member, len(v.Members))
		for i, c := range v.Channels {
			channels[i] = pb.Channel{Id: uint32(c.ID), Name: c.Name, Description: c.Description, Bitrate: c.Bitrate}
			s.Channels[i] = &channels[i]
		}
		for i, m := range v.Members {
			members[i] = pb.Member{Id: uint32(m.ID), Channel: uint32(m.Channel), Nickname: m.Nickname, Instance: m.Instance, Muted: m.Muted, Deafened: m.Deafened, Epoch: m.Epoch}
			s.Members[i] = &members[i]
		}
		return s, nil
	case Command:
		if kind == MoveKind || kind == ChatKind || kind == VoiceStateKind {
			return &pb.Command{Channel: uint32(v.Channel), Text: v.Text, Muted: v.Muted, Deafened: v.Deafened}, nil
		}
	case Reply:
		if kind == ReplyKind {
			return &pb.Reply{Code: uint32(v.Code)}, nil
		}
	case Message:
		if kind == MessageKind {
			return &pb.Message{Channel: uint32(v.Channel), Sender: uint32(v.Sender), Nickname: v.Nickname, Text: v.Text}, nil
		}
	}
	return nil, ErrPacket
}

func decodeBody(f Frame, value any) error {
	switch out := value.(type) {
	case *WatchResources:
		if out == nil || f.Kind != WatchResourcesKind {
			return ErrPacket
		}
		var v pb.WatchResources
		if err := unmarshal.Unmarshal(f.Body, &v); err != nil {
			return err
		}
		*out = WatchResources{AllChannels: v.AllChannels, AllMembers: v.AllMembers}
	case *CreateChannel:
		if out == nil || f.Kind != CreateChannelKind {
			return ErrPacket
		}
		var v pb.CreateChannel
		if err := unmarshal.Unmarshal(f.Body, &v); err != nil {
			return err
		}
		*out = CreateChannel{Name: v.Name, Description: v.Description, Bitrate: v.Bitrate}
	case *UpdateChannel:
		if out == nil || f.Kind != UpdateChannelKind {
			return ErrPacket
		}
		var v pb.UpdateChannel
		if err := unmarshal.Unmarshal(f.Body, &v); err != nil {
			return err
		}
		if v.Id > math.MaxUint16 {
			return ErrPacket
		}
		*out = UpdateChannel{ID: uint16(v.Id), Name: v.Name, Description: v.Description, Bitrate: v.Bitrate}
	case *DeleteChannel:
		if out == nil || f.Kind != DeleteChannelKind {
			return ErrPacket
		}
		var v pb.DeleteChannel
		if err := unmarshal.Unmarshal(f.Body, &v); err != nil {
			return err
		}
		if v.Id > math.MaxUint16 {
			return ErrPacket
		}
		*out = DeleteChannel{ID: uint16(v.Id)}
	case *Hello:
		if out == nil || f.Kind != HelloKind {
			return ErrPacket
		}
		var v pb.Hello
		if err := unmarshal.Unmarshal(f.Body, &v); err != nil {
			return err
		}
		*out = Hello{Nickname: v.Nickname, Password: v.Password, PublicKey: v.PublicKey, Signature: v.Signature}
	case *ClaimOwner:
		if out == nil || f.Kind != ClaimOwnerKind {
			return ErrPacket
		}
		var v pb.ClaimOwner
		if err := unmarshal.Unmarshal(f.Body, &v); err != nil {
			return err
		}
		if len(v.Token) > 64 {
			return ErrPacket
		}
		*out = ClaimOwner{Token: v.Token}
	case *Command:
		if out == nil || (f.Kind != MoveKind && f.Kind != ChatKind && f.Kind != VoiceStateKind) {
			return ErrPacket
		}
		var v pb.Command
		if err := unmarshal.Unmarshal(f.Body, &v); err != nil {
			return err
		}
		if v.Channel > math.MaxUint16 {
			return ErrPacket
		}
		*out = Command{Channel: uint16(v.Channel), Text: v.Text, Muted: v.Muted, Deafened: v.Deafened}
	case *Reply:
		if out == nil || f.Kind != ReplyKind {
			return ErrPacket
		}
		var v pb.Reply
		if err := unmarshal.Unmarshal(f.Body, &v); err != nil {
			return err
		}
		if v.Code > math.MaxUint8 {
			return ErrPacket
		}
		*out = Reply{Code: uint8(v.Code)}
	case *Message:
		if out == nil || f.Kind != MessageKind {
			return ErrPacket
		}
		var v pb.Message
		if err := unmarshal.Unmarshal(f.Body, &v); err != nil {
			return err
		}
		if v.Channel > math.MaxUint16 || v.Sender > math.MaxUint16 {
			return ErrPacket
		}
		*out = Message{Channel: uint16(v.Channel), Sender: uint16(v.Sender), Nickname: v.Nickname, Text: v.Text}
	case *State:
		if out == nil || (f.Kind != StateKind && f.Kind != WelcomeKind) || !boundedState(f.Body) {
			return ErrPacket
		}
		var v pb.State
		if err := unmarshal.Unmarshal(f.Body, &v); err != nil {
			return err
		}
		if v.Self > math.MaxUint16 || v.DefaultChannel > math.MaxUint16 {
			return ErrPacket
		}
		s := State{Name: v.Name, Self: uint16(v.Self), Epoch: v.Epoch, Channels: make([]Channel, len(v.Channels)), Members: make([]Member, len(v.Members)), IdentityUID: v.IdentityUid, ServerRole: v.ServerRole, CanClaimOwner: v.CanClaimOwner}
		s.CanManageChannels = v.CanManageChannels
		s.CanConfigureChannelAudio = v.CanConfigureChannelAudio
		s.CanWatchResources, s.Revision = v.CanWatchResources, v.Revision
		s.AllChannels, s.AllMembers, s.DefaultChannel = v.AllChannels, v.AllMembers, uint16(v.DefaultChannel)
		for i, c := range v.Channels {
			if c == nil || c.Id > math.MaxUint16 {
				return ErrPacket
			}
			s.Channels[i] = Channel{ID: uint16(c.Id), Name: c.Name, Description: c.Description, Bitrate: c.Bitrate}
		}
		for i, m := range v.Members {
			if m == nil || m.Id > math.MaxUint16 || m.Channel > math.MaxUint16 {
				return ErrPacket
			}
			s.Members[i] = Member{ID: uint16(m.Id), Channel: uint16(m.Channel), Nickname: m.Nickname, Instance: m.Instance, Muted: m.Muted, Deafened: m.Deafened, Epoch: m.Epoch}
		}
		*out = s
	default:
		return ErrPacket
	}
	return nil
}

// Count repeated entries before protobuf allocates their message objects. A
// small frame filled with empty submessages must not bypass membership limits.
func boundedState(data []byte) bool {
	channels, members := 0, 0
	for len(data) > 0 {
		num, typ, n := protowire.ConsumeTag(data)
		if n < 0 || typ == protowire.StartGroupType || typ == protowire.EndGroupType {
			return false
		}
		data = data[n:]
		n = protowire.ConsumeFieldValue(num, typ, data)
		if n < 0 {
			return false
		}
		if typ == protowire.BytesType {
			if num == 4 {
				channels++
			}
			if num == 5 {
				members++
			}
			if channels > MaxChannels || members > MaxMembers {
				return false
			}
		}
		data = data[n:]
	}
	return true
}
