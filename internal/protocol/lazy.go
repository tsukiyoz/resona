package protocol

import (
	"context"
	"errors"

	"github.com/tsukiyoz/resona/internal/client"
)

// LazyConnector defers protocol-specific setup until that adapter is selected.
// Factories do local configuration only; network setup belongs to Connect.
type LazyConnector struct {
	init   func() (client.RemoteConnector, error)
	gate   chan struct{}
	target client.RemoteConnector
}

func NewLazyConnector(init func() (client.RemoteConnector, error)) *LazyConnector {
	return &LazyConnector{init: init, gate: make(chan struct{}, 1)}
}

func (c *LazyConnector) Connect(ctx context.Context, profile client.ServerProfile, password string, update func(client.RemoteState)) (client.RemoteConnection, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case c.gate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		<-c.gate
		return nil, err
	}
	if c.target == nil {
		target, err := c.init()
		if err != nil {
			<-c.gate
			return nil, err
		}
		if target == nil {
			<-c.gate
			return nil, errors.New("protocol initialization returned no connector")
		}
		c.target = target
	}
	target := c.target
	<-c.gate
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return target.Connect(ctx, profile, password, update)
}
