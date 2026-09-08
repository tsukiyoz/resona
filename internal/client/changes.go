package client

import "sync"

// SubscribeChanges provides coalesced invalidations, not a stream of snapshots.
// Consumers read the latest state after a signal; slow UIs never block protocol work.
func (s *Service) SubscribeChanges() (<-chan struct{}, func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.subscribers == nil {
		s.subscribers = make(map[chan struct{}]struct{})
	}
	ch := make(chan struct{}, 1)
	s.subscribers[ch] = struct{}{}
	ch <- struct{}{}
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			s.mu.Lock()
			delete(s.subscribers, ch)
			s.mu.Unlock()
		})
	}
}

func (s *Service) notifyChangedLocked() {
	for ch := range s.subscribers {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}
