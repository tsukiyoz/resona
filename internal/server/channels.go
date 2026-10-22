package server

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	w "github.com/tsukiyoz/resona/internal/nativewire"
)

type channelRecord struct {
	Version  int         `json:"version"`
	Next     uint32      `json:"next"`
	Default  uint16      `json:"default_id"`
	Channels []w.Channel `json:"channels"`
}

func validChannels(channels []w.Channel) bool {
	if len(channels) == 0 || len(channels) > w.MaxChannels {
		return false
	}
	seen := map[uint16]bool{}
	budget := 0
	for _, ch := range channels {
		if !w.ValidChannelBitrate(ch.Bitrate) {
			return false
		}
		if ch.ID == 0 || seen[ch.ID] || !validName(ch.Name, 100) || !utf8.ValidString(ch.Description) || len(ch.Description) > 1024 || strings.ContainsRune(ch.Description, 0) {
			return false
		}
		seen[ch.ID] = true
		budget += len(ch.Name) + len(ch.Description)
	}
	return budget <= 16*1024
}

func (s *Server) manageChannel(p *peer, kind uint8, id uint16, name, description string, audioBitrate ...uint32) uint8 {
	ctx := context.Background()
	if p.conn != nil {
		ctx = p.conn.Context()
	}
	return s.ManageChannel(ctx, p.identity, kind, id, name, description, audioBitrate...)
}

// ManageChannel is the shared authorized mutation boundary for native commands
// and future admin transports. The actor is an authenticated identity, not a name.
func (s *Server) ManageChannel(parent context.Context, identity string, kind uint8, id uint16, name, description string, audioBitrate ...uint32) uint8 {
	return s.mutateChannel(parent, identity, kind, id, 0, name, description, audioBitrate...)
}

// ReorderChannel moves a channel before beforeID (zero means the end).
// Only the authenticated owner can change the server-wide persisted order.
func (s *Server) ReorderChannel(ctx context.Context, identity string, id, beforeID uint16) uint8 {
	return s.mutateChannel(ctx, identity, w.ReorderChannelKind, id, beforeID, "", "")
}

func (s *Server) mutateChannel(parent context.Context, identity string, kind uint8, id, beforeID uint16, name, description string, audioBitrate ...uint32) uint8 {
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	s.channelMu.Lock()
	defer s.channelMu.Unlock()
	if ctx.Err() != nil {
		return w.StorageFailed
	}
	if !s.config.Store.Allowed(identity, "channels", "write", "*") {
		return w.PermissionDenied
	}
	var bitrate uint32
	if len(audioBitrate) != 0 {
		bitrate = audioBitrate[0]
	}
	if !w.ValidChannelBitrate(bitrate) {
		return w.Rejected
	}
	name = strings.TrimSpace(name)
	code := uint8(w.OK)
	err := s.config.Store.updateContext(ctx, func(r *resourceRecord) error {
		if !authorized(*r, identity, "channels", "write", "*") {
			code = w.PermissionDenied
			return ErrPermission
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		channels := append([]w.Channel(nil), r.Channels.Channels...)
		index := -1
		for i, ch := range channels {
			if ch.ID == id {
				index = i
			}
		}
		next := r.Channels.Next
		switch kind {
		case w.CreateChannelKind:
			if next > 65535 || len(channels) >= w.MaxChannels {
				code = w.ChannelLimit
				return errors.New("channel limit")
			}
			if bitrate == 0 {
				bitrate = 32000
			}
			channels = append(channels, w.Channel{ID: uint16(next), Name: name, Description: description, Bitrate: bitrate})
			next++
		case w.UpdateChannelKind:
			if index < 0 {
				code = w.WrongChannel
				return errors.New("channel not found")
			}
			channels[index].Name, channels[index].Description = name, description
			if bitrate != 0 {
				channels[index].Bitrate = bitrate
			}
		case w.DeleteChannelKind:
			if index < 0 {
				code = w.WrongChannel
				return errors.New("channel not found")
			}
			if id == r.Channels.Default {
				code = w.DefaultChannel
				return errors.New("default channel")
			}
			for _, other := range s.peers {
				if other.member.Channel == id {
					code = w.ChannelNotEmpty
					return errors.New("channel not empty")
				}
			}
			channels = append(channels[:index], channels[index+1:]...)
		case w.ReorderChannelKind:
			if index < 0 {
				code = w.WrongChannel
				return errors.New("channel not found")
			}
			if beforeID != id {
				moved := channels[index]
				channels = append(channels[:index], channels[index+1:]...)
				position := len(channels)
				if beforeID != 0 {
					position = -1
					for i, ch := range channels {
						if ch.ID == beforeID {
							position = i
						}
					}
					if position < 0 {
						code = w.WrongChannel
						return errors.New("target channel not found")
					}
				}
				channels = append(channels, w.Channel{})
				copy(channels[position+1:], channels[position:])
				channels[position] = moved
			}
		default:
			code = w.Rejected
			return errors.New("invalid channel operation")
		}
		if !validChannels(channels) {
			code = w.Rejected
			return errors.New("invalid channel metadata")
		}
		if kind == w.DeleteChannelKind {
			s.deletingChannel = id
		}
		r.Channels.Channels, r.Channels.Next = channels, next
		return nil
	})
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deletingChannel = 0
	if err == nil {
		r, revision := s.config.Store.state()
		s.config.Channels, s.resourceRevision = r.Channels.Channels, revision
		s.defaultChannel = r.Channels.Default
		s.broadcastLocked(nil)
	}
	if code != w.OK {
		return code
	}
	if err != nil {
		return w.StorageFailed
	}
	return w.OK
}

func (s *Server) channelCommand(p *peer, f w.Frame) bool {
	var id, beforeID uint16
	var bitrate uint32
	var name, description string
	switch f.Kind {
	case w.CreateChannelKind:
		var v w.CreateChannel
		if w.Decode(f, &v) != nil {
			return false
		}
		name, description = v.Name, v.Description
		bitrate = v.Bitrate
	case w.UpdateChannelKind:
		var v w.UpdateChannel
		if w.Decode(f, &v) != nil {
			return false
		}
		id, name, description = v.ID, v.Name, v.Description
		bitrate = v.Bitrate
	case w.DeleteChannelKind:
		var v w.DeleteChannel
		if w.Decode(f, &v) != nil {
			return false
		}
		id = v.ID
	case w.ReorderChannelKind:
		var v w.ReorderChannel
		if w.Decode(f, &v) != nil {
			return false
		}
		id, beforeID = v.ID, v.BeforeID
	}
	code := uint8(w.RateLimited)
	if p.commandBucket.take(2, 4) {
		if f.Kind == w.ReorderChannelKind {
			code = s.ReorderChannel(p.conn.Context(), p.identity, id, beforeID)
		} else {
			code = s.manageChannel(p, f.Kind, id, name, description, bitrate)
		}
	}
	s.mu.Lock()
	s.enqueueLocked(p, w.ReplyKind, f.Request, w.Reply{Code: code})
	s.mu.Unlock()
	return true
}
