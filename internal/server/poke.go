package server

import w "github.com/tsukiyoz/resona/internal/nativewire"

// Attention stays in the bounded control queues, outside chat persistence and voice.
func (s *Server) pokeCommand(p *peer, f w.Frame) bool {
	var cmd w.Poke
	if w.Decode(f, &cmd) != nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if p.removed {
		return false
	}
	code := uint8(w.OK)
	target := s.peers[cmd.Target]
	switch {
	case target == nil || target == p || target.removed || !target.receivePokes || target.member.Instance != cmd.Instance:
		code = w.MemberUnavailable
	case !p.pokeSend.take(0.2, 1) || !target.pokeReceive.take(0.5, 1):
		code = w.RateLimited
	default:
		packet, err := w.Pack(w.PokedKind, 0, w.Poked{Sender: p.member.ID, Instance: p.member.Instance, Nickname: p.member.Nickname})
		if err != nil {
			return false
		}
		select {
		case target.out <- packet:
		default:
			code = w.DeliveryUnknown
		}
	}
	s.enqueueLocked(p, w.ReplyKind, f.Request, w.Reply{Code: code})
	return true
}
