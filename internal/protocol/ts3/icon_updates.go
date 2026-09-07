package ts3

func (s *connection) publishIcon(id, dataURL string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing || s.state.state.Closed {
		return
	}
	s.state.iconCache[id] = dataURL
	for key, channel := range s.state.channels {
		if channel.IconID == id {
			channel.IconDataURL = dataURL
			s.state.channels[key] = channel
		}
	}
	if s.state.ready() {
		snapshot := s.state.snapshot()
		snapshot.Events, snapshot.Messages = nil, nil
		s.update(snapshot)
	}
}
