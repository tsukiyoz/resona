package server

import (
	"log/slog"
	"net"

	w "github.com/tsukiyoz/resona/internal/nativewire"
)

func connectionLogger(c w.Connection, member w.Member) *slog.Logger {
	remoteAddr, remoteIP := "unknown", "unknown"
	if remote, ok := c.(interface{ RemoteAddr() net.Addr }); ok {
		if addr := remote.RemoteAddr(); addr != nil {
			remoteAddr = addr.String()
			if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
				remoteIP = host
			}
		}
	}
	return slog.With("remote_addr", remoteAddr, "remote_ip", remoteIP,
		"connection_name", member.Nickname, "member_id", member.ID, "session", member.Instance)
}

// Snapshot mutable identity under Server.mu, then emit outside the routing lock.
func (p *peer) identityLoggerLocked() *slog.Logger {
	logger := p.logger
	if logger == nil {
		logger = slog.With("member_id", p.member.ID, "session", p.member.Instance)
	}
	kind := "guest"
	if p.account.UID != "" {
		kind = "account"
	}
	return logger.With("name", p.member.Nickname, "identity_type", kind,
		"account_uid", p.account.UID, "account_name", p.account.Username)
}
