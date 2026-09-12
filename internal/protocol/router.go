package protocol

import (
	"context"
	"errors"
	"github.com/tsukiyoz/resona/internal/client"
)

type Router struct{ TS3, Native client.RemoteConnector }

func (r Router) Connect(ctx context.Context, p client.ServerProfile, password string, update func(client.RemoteState)) (client.RemoteConnection, error) {
	var target client.RemoteConnector
	switch p.Protocol {
	case "", "ts3":
		target = r.TS3
	case "resona", "resona-noise":
		target = r.Native
	default:
		return nil, errors.New("unknown protocol")
	}
	if target == nil {
		return nil, errors.New("protocol unavailable")
	}
	return target.Connect(ctx, p, password, update)
}
