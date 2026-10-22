package server

import (
	"context"
	"errors"
	"strings"
	"testing"

	w "github.com/tsukiyoz/resona/internal/nativewire"
)

func TestCasbinRolesPersistAndApplyToNativeChannelManagement(t *testing.T) {
	s, owner, dir := channelTestServer(t)
	admin, member := strings.Repeat("b", 64), strings.Repeat("c", 64)
	ctx := context.Background()
	_, revision := s.config.Store.state()
	if err := s.config.Store.SetRole(ctx, member, admin, "admin", revision); !errors.Is(err, ErrPermission) {
		t.Fatal(err)
	}
	if err := s.config.Store.SetRole(ctx, owner.identity, admin, "admin", revision); err != nil {
		t.Fatal(err)
	}
	if err := s.config.Store.SetRole(ctx, owner.identity, member, "admin", revision); !errors.Is(err, ErrConflict) {
		t.Fatal("stale revision accepted", err)
	}
	if code := s.ManageChannel(ctx, admin, w.CreateChannelKind, 0, "Admin channel", ""); code != w.OK {
		t.Fatal(code)
	}
	if !s.config.Store.Allowed(admin, "members", "write", "member") || s.config.Store.Allowed(admin, "members", "write", "owner") || s.config.Store.Allowed(admin, "roles", "write", "*") {
		t.Fatal("incorrect Casbin target role policy")
	}
	if err := s.config.Store.SetRole(ctx, owner.identity, owner.identity, "member", 0); !errors.Is(err, ErrInvalid) {
		t.Fatal("owner changed", err)
	}
	s.config.Store.Close()
	s.config.Store = testStore(t, dir)
	if role, _ := s.config.Store.Status(admin); role != "admin" {
		t.Fatal("role lost on restart")
	}
	if err := s.config.Store.SetRole(ctx, owner.identity, admin, "member", 0); err != nil {
		t.Fatal(err)
	}
	if code := s.ManageChannel(ctx, admin, w.UpdateChannelKind, 3, "Revoked", ""); code != w.PermissionDenied {
		t.Fatal("revoked admin still allowed", code)
	}
}
