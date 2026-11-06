package server

import w "github.com/tsukiyoz/resona/internal/nativewire"

func channelByID(r resourceRecord, id uint16) (w.Channel, bool) {
	for _, ch := range r.Channels.Channels {
		if ch.ID == id {
			return ch, true
		}
	}
	return w.Channel{}, false
}

func canEnterChannel(r resourceRecord, p *peer, ch w.Channel) bool {
	role := roleOf(r, p.principal())
	return !ch.AdminOnly || role == "owner" || role == "admin" || p.temporaryChannel == ch.ID
}

// A temporary admission belongs to this connection and ends on an actual move.
// Every move uses the same epoch/chat boundary as a self-initiated channel join.
func (s *Server) movePeerLocked(p *peer, channel uint16, temporary bool) bool {
	if p.member.Channel == channel {
		return true
	}
	if p.epoch == ^uint32(0) {
		return false
	}
	p.temporaryChannel = 0
	if temporary {
		p.temporaryChannel = channel
	}
	p.epoch++
	p.member.Epoch, p.member.Channel = p.epoch, channel
	s.restartChatLocked(p)
	return true
}

func (s *Server) enforceChannelAccessLocked(r resourceRecord) {
	for _, p := range s.peers {
		if p.removed {
			continue
		}
		ch, found := channelByID(r, p.member.Channel)
		if found && canEnterChannel(r, p, ch) {
			continue
		}
		if !s.movePeerLocked(p, r.Channels.Default, false) {
			p.removed, p.member.Muted, p.member.Deafened = true, true, true
			if p.conn != nil {
				_ = p.conn.CloseWithError(w.AccountRevoked, "session epoch exhausted")
			}
		}
	}
}
