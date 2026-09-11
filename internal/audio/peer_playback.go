package audio

// PeerPlayback belongs to one member instance in one Engine/session.
type PeerPlayback struct {
	Instance string
	Volume   int
	Muted    bool
}

// SetPeerPlayback publishes a copy; the mixer reads one immutable table per frame.
// It performs no device operations and never calls the service back.
func (e *Engine) SetPeerPlayback(peers map[uint16]PeerPlayback) {
	copy := make(map[uint16]PeerPlayback, len(peers))
	for id, peer := range peers {
		peer.Volume = max(0, min(200, peer.Volume))
		copy[id] = peer
	}
	e.peers.Store(&copy)
}

func peerGain(peers *map[uint16]PeerPlayback, id uint16, instance string) (float32, bool) {
	if peers == nil {
		return 1, true
	}
	peer, ok := (*peers)[id]
	if !ok || peer.Instance != instance {
		return 0, false
	}
	if peer.Muted {
		return 0, true
	}
	return float32(peer.Volume) / 100, true
}
