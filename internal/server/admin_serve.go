package server

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// ServeWithAdmin binds management before accepting voice clients and joins both
// listeners. Any unexpected listener failure cancels its sibling.
func (s *Server) ServeWithAdmin(parent context.Context, address, origin string) error {
	if address == "" {
		return s.Serve(parent)
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		_ = s.listener.Close()
		s.config.Store.Close()
		return err
	}
	if origin == "" {
		origin = "http://" + listener.Addr().String()
	}
	console, err := NewAdminConsole(s, origin)
	if err != nil {
		listener.Close()
		_ = s.listener.Close()
		s.config.Store.Close()
		return err
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	h := &http.Server{Handler: console, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10, BaseContext: func(net.Listener) context.Context { return ctx }}
	done := make(chan error, 1)
	go func() {
		err := h.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		done <- err
		cancel()
	}()
	slog.Info("admin listening", "address", listener.Addr().String(), "origin", origin)
	voiceErr := s.Serve(ctx)
	cancel()
	shutdown, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	if err := h.Shutdown(shutdown); err != nil {
		_ = h.Close()
	}
	return errors.Join(voiceErr, <-done)
}
