package desktopipc

import (
	"context"

	"github.com/tsukiyoz/resona/internal/client"
	"github.com/tsukiyoz/resona/internal/diagnostics"
)

func dispatchRead(ctx context.Context, s *client.Service, req request) (any, error) {
	if req.Method == "ExportDiagnostics" {
		s.RecordVoiceDiagnostics()
		return diagnostics.ExportDefault(ctx)
	}
	var p struct {
		SessionID string `json:"sessionID"`
		ChannelID string `json:"channelID"`
		UserID    string `json:"userID"`
		Ref       string `json:"ref"`
	}
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	switch req.Method {
	case "GetIconResource":
		return s.GetIconResource(ctx, p.SessionID, p.Ref)
	case "GetChannelDetails":
		return s.GetChannelDetails(ctx, p.SessionID, p.ChannelID)
	default:
		return s.GetUserDetails(ctx, p.SessionID, p.UserID)
	}
}
