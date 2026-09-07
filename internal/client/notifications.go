package client

import (
	"crypto/rand"
	"time"
)

const maxNotifications = 64

type RemoteEvent struct {
	Kind      string
	UserID    string
	ChannelID string
}

type Notification struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	ChannelID string `json:"channelID"`
	CreatedAt string `json:"createdAt"`
}

func (s *Service) applyRemoteEventsLocked(events []RemoteEvent) {
	if s.state.Session.SelfID == "" || s.state.Session.ChannelID == "" {
		return
	}
	for _, event := range events {
		if event.Kind != "member_joined" && event.Kind != "member_left" {
			continue
		}
		if event.UserID == "" || event.UserID == s.state.Session.SelfID || event.ChannelID == "" || event.ChannelID != s.state.Session.ChannelID {
			continue
		}
		s.addNotificationLocked(event.Kind, event.ChannelID)
	}
}

func (s *Service) addNotificationLocked(kind, channelID string) {
	if s.shutdown {
		return
	}
	if len(s.state.Notifications) == maxNotifications {
		copy(s.state.Notifications, s.state.Notifications[1:])
		s.state.Notifications = s.state.Notifications[:maxNotifications-1]
	}
	s.state.Notifications = append(s.state.Notifications, Notification{
		ID: rand.Text(), Kind: kind, ChannelID: channelID,
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	})
}
