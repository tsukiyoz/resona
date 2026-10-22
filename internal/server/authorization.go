package server

import (
	"context"
	"errors"
	"slices"
	"sync"

	"github.com/casbin/casbin/v2"
	"github.com/casbin/casbin/v2/model"
)

var (
	ErrPermission = errors.New("permission denied")
	ErrConflict   = errors.New("resources changed; reload before saving")
	ErrInvalid    = errors.New("invalid management request")
	ErrNotFound   = errors.New("resource no longer exists")
)

// Policies are immutable after construction. KV stores role assignments, not a
// second writable policy database; changing a role and its resources stays one CAS.
var authorization = sync.OnceValues(func() (*casbin.SyncedEnforcer, error) {
	m, err := model.NewModelFromString(`[request_definition]
r = sub, obj, act, target
[policy_definition]
p = sub, obj, act, target
[role_definition]
g = _, _
[policy_effect]
e = some(where (p.eft == allow))
[matchers]
m = g(r.sub, p.sub) && r.obj == p.obj && r.act == p.act && (p.target == "*" || r.target == p.target)
`)
	if err != nil {
		return nil, err
	}
	e, err := casbin.NewSyncedEnforcer(m)
	if err != nil {
		return nil, err
	}
	for _, p := range [][]string{
		{"admin", "panel", "read", "*"},
		{"admin", "channels", "write", "*"},
		{"admin", "members", "write", "member"},
		{"owner", "members", "write", "admin"},
		{"owner", "roles", "write", "*"},
		{"owner", "settings", "write", "*"},
		{"owner", "tokens", "write", "*"},
	} {
		if _, err = e.AddPolicy(p); err != nil {
			return nil, err
		}
	}
	_, err = e.AddGroupingPolicy("owner", "admin")
	return e, err
})

func roleOf(r resourceRecord, identity string) string {
	if identity != "" && identity == r.Owner {
		return "owner"
	}
	if slices.Contains(r.Admins, identity) {
		return "admin"
	}
	return "member"
}

func authorized(r resourceRecord, identity, object, action, target string) bool {
	e, err := authorization()
	if err != nil || !validIdentity(identity) {
		return false
	}
	ok, err := e.Enforce(roleOf(r, identity), object, action, target)
	return err == nil && ok
}

func (s *Store) Allowed(identity, object, action, target string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return authorized(s.record, identity, object, action, target)
}

// SetRole rechecks the authenticated actor against the current CAS draft.
// Ownership is immutable; only delegation to admin and revocation to member exist.
func (s *Store) SetRole(ctx context.Context, actor, identity, role string, revision uint64) error {
	return s.updateAtContext(ctx, revision, func(r *resourceRecord) error {
		if !authorized(*r, actor, "roles", "write", "*") {
			return ErrPermission
		}
		if !validIdentity(identity) || identity == r.Owner || (role != "admin" && role != "member") {
			return ErrInvalid
		}
		r.Admins = slices.DeleteFunc(r.Admins, func(id string) bool { return id == identity })
		if role == "admin" {
			if len(r.Admins) >= 64 {
				return ErrInvalid
			}
			r.Admins = append(r.Admins, identity)
			slices.Sort(r.Admins)
		}
		return nil
	})
}
