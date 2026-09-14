package client

import "context"

type ResourceInterest struct {
	Active      bool `json:"active"`
	AllChannels bool `json:"allChannels"`
	AllMembers  bool `json:"allMembers"`
}

type resourceWatcher interface {
	SetResourceInterest(context.Context, bool, bool) error
}

// Keep transport waits outside the service lock; each call captures one session.
// IPC serializes calls and reapplies the latest interest after a session change.
func (s *Service) SyncResources(ctx context.Context, interest ResourceInterest) error {
	s.mu.Lock()
	connection := s.connection
	s.mu.Unlock()
	if watcher, ok := connection.(resourceWatcher); ok {
		return watcher.SetResourceInterest(ctx, interest.Active && interest.AllChannels, interest.Active && interest.AllChannels && interest.AllMembers)
	}
	return nil
}

func (s *Service) ResourceSessionID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Session.Mode != "connected" || s.connection == nil {
		return ""
	}
	return s.state.Session.ID
}

// Session changes stay observable without cloning resource arrays or history.
func (s *Service) GetSessionState() Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.Session
}

// Background sounds need no channel arrays, message history or icon resources.
type NotificationUpdate struct {
	Session       Session        `json:"session"`
	Notifications []Notification `json:"notifications"`
}

func (s *Service) GetNotificationUpdate(after string) *NotificationUpdate {
	s.mu.Lock()
	defer s.mu.Unlock()
	notifications := s.state.Notifications
	if len(notifications) == 0 || notifications[len(notifications)-1].ID == after {
		return nil
	}
	return &NotificationUpdate{Session: s.state.Session, Notifications: append([]Notification(nil), notifications...)}
}
