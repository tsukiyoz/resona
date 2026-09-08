package ts3

import (
	"context"
	"errors"

	"github.com/tsukiyoz/resona/internal/client"
)

func (s *connection) ReadIconResource(ctx context.Context, ref string) (client.IconResource, error) {
	authorized := func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		if ref == "" || s.closing || s.state.state.Closed {
			return false
		}
		for _, channel := range s.state.channels {
			if channel.IconRef == ref {
				return true
			}
		}
		return false
	}
	if s.iconStore == nil || !authorized() {
		return client.IconResource{}, errors.New("图标资源不可用")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(s.lifetime, cancel)
	defer stop()
	data, err := s.iconStore.Read(ctx, ref)
	if err != nil {
		return client.IconResource{}, errors.New("图标资源已失效")
	}
	if ctx.Err() != nil || !authorized() {
		return client.IconResource{}, context.Canceled
	}
	return client.IconResource{Ref: ref, DataURL: data}, nil
}

func (s *connection) publishIcon(id, ref string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing || s.state.state.Closed {
		return
	}
	s.state.iconCache[id] = ref
	for key, channel := range s.state.channels {
		if channel.IconID == id {
			channel.IconRef = ref
			s.state.channels[key] = channel
		}
	}
	if s.state.ready() {
		snapshot := s.state.snapshot()
		snapshot.Events, snapshot.Messages = nil, nil
		s.update(snapshot)
	}
}
