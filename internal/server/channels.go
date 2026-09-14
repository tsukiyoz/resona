package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/tsukiyoz/resona/internal/nativeidentity"
	w "github.com/tsukiyoz/resona/internal/nativewire"
)

type channelRecord struct {
	Version  int         `json:"version"`
	Next     uint32      `json:"next"`
	Channels []w.Channel `json:"channels"`
}

// ChannelStore is owned by one running server under the CLI access lock.
// The first entry is the default channel; IDs are never reused after deletion.
type ChannelStore struct {
	path   string
	record channelRecord
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

func OpenChannelStore(path string, seed []w.Channel) (*ChannelStore, error) {
	s := &ChannelStore{path: path}
	data, err := nativeidentity.ReadPrivate(path, 65536)
	if errors.Is(err, os.ErrNotExist) {
		if !validChannels(seed) {
			return nil, errors.New("invalid channel seed")
		}
		s.record = channelRecord{Version: 1, Next: 1, Channels: append([]w.Channel(nil), seed...)}
		for _, ch := range seed {
			s.record.Next = max(s.record.Next, uint32(ch.ID)+1)
		}
		data, _ = json.Marshal(s.record)
		if err = nativeidentity.WriteNew(path, data); err != nil {
			return nil, err
		}
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if json.Unmarshal(data, &s.record) != nil || s.record.Version != 1 || !validChannels(s.record.Channels) || s.record.Next == 0 || s.record.Next > 65536 {
		return nil, errors.New("invalid channel store; original preserved")
	}
	for _, ch := range s.record.Channels {
		if uint32(ch.ID) >= s.record.Next {
			return nil, errors.New("invalid channel ID sequence")
		}
	}
	return s, nil
}

func (s *ChannelStore) save(channels []w.Channel, next uint32) (bool, error) {
	record := channelRecord{Version: 1, Next: next, Channels: channels}
	data, err := json.Marshal(record)
	if err != nil {
		return false, err
	}
	err = nativeidentity.ReplacePrivate(s.path, data)
	published := err == nil
	if err != nil {
		// A directory fsync error can occur after rename. Reflect published state
		// in memory while still returning an unconfirmed persistence failure.
		actual, readErr := nativeidentity.ReadPrivate(s.path, 65536)
		published = readErr == nil && bytes.Equal(actual, data)
	}
	if published {
		s.record = record
	}
	return published, err
}

func (s *Server) manageChannel(p *peer, kind uint8, id uint16, name, description string, audioBitrate ...uint32) uint8 {
	s.channelMu.Lock()
	defer s.channelMu.Unlock()
	role, _ := s.config.Ownership.Status(p.identity)
	if role != "owner" || s.config.ChannelStore == nil {
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
	s.mu.Lock()
	channels := append([]w.Channel(nil), s.config.Channels...)
	index := -1
	for i, ch := range channels {
		if ch.ID == id {
			index = i
		}
	}
	next := s.config.ChannelStore.record.Next
	switch kind {
	case w.CreateChannelKind:
		if next > 65535 || len(channels) >= w.MaxChannels {
			s.mu.Unlock()
			return w.ChannelLimit
		}
		if bitrate == 0 {
			bitrate = 32000
		}
		channels = append(channels, w.Channel{ID: uint16(next), Name: name, Description: description, Bitrate: bitrate})
		next++
	case w.UpdateChannelKind:
		if index < 0 {
			s.mu.Unlock()
			return w.WrongChannel
		}
		channels[index].Name, channels[index].Description = name, description
		if bitrate != 0 {
			channels[index].Bitrate = bitrate
		}
	case w.DeleteChannelKind:
		if index < 0 {
			s.mu.Unlock()
			return w.WrongChannel
		}
		if index == 0 {
			s.mu.Unlock()
			return w.DefaultChannel
		}
		for _, other := range s.peers {
			if other.member.Channel == id {
				s.mu.Unlock()
				return w.ChannelNotEmpty
			}
		}
		channels = append(channels[:index], channels[index+1:]...)
	default:
		s.mu.Unlock()
		return w.Rejected
	}
	if !validChannels(channels) {
		s.mu.Unlock()
		return w.Rejected
	}
	if kind == w.DeleteChannelKind {
		s.deletingChannel = id
	}
	s.mu.Unlock()
	// Disk I/O never holds the voice routing mutex. While a deletion is being
	// persisted, Move rejects that target so an occupied channel cannot vanish.
	published, err := s.config.ChannelStore.save(channels, next)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deletingChannel = 0
	if published {
		s.config.Channels = channels
		s.broadcastLocked(nil)
	}
	if err != nil {
		return w.StorageFailed
	}
	return w.OK
}

func (s *Server) channelCommand(p *peer, f w.Frame) bool {
	var id uint16
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
	}
	code := uint8(w.RateLimited)
	if p.commandBucket.take(2, 4) {
		code = s.manageChannel(p, f.Kind, id, name, description, bitrate)
	}
	s.mu.Lock()
	s.enqueueLocked(p, w.ReplyKind, f.Request, w.Reply{Code: code})
	s.mu.Unlock()
	return true
}
