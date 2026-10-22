package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"slices"
	"strings"
	"time"
)

type AdminCredential struct {
	ID       string    `json:"id"`
	Identity string    `json:"identity"`
	Label    string    `json:"label"`
	Expires  time.Time `json:"expires"`
	Digest   string    `json:"digest,omitempty"`
}

// IssueAdminCredential returns the only copy of a random bearer secret. Offline
// CLI issuance uses the current owner as actor; HTTP must supply its session UID.
func (s *Store) IssueAdminCredential(ctx context.Context, actor, identity, label string, ttl time.Duration, revision uint64) (AdminCredential, string, error) {
	secret := rand.Text() + rand.Text()
	hash := sha256.Sum256([]byte(secret))
	c := AdminCredential{ID: rand.Text(), Identity: identity, Label: strings.TrimSpace(label), Expires: time.Now().UTC().Add(ttl), Digest: hex.EncodeToString(hash[:])}
	err := s.updateAtContext(ctx, revision, func(r *resourceRecord) error {
		if !authorized(*r, actor, "tokens", "write", "*") {
			return ErrPermission
		}
		if !authorized(*r, identity, "panel", "read", "*") || !validName(c.Label, 64) || ttl < time.Hour || ttl > 90*24*time.Hour {
			return ErrInvalid
		}
		r.Credentials = slices.DeleteFunc(r.Credentials, func(v AdminCredential) bool { return !time.Now().Before(v.Expires) })
		if len(r.Credentials) >= 64 {
			return ErrInvalid
		}
		r.Credentials = append(r.Credentials, c)
		return nil
	})
	c.Digest = ""
	if err != nil {
		return AdminCredential{}, "", err
	}
	return c, secret, nil
}

func (s *Store) RevokeAdminCredential(ctx context.Context, actor, id string, revision uint64) error {
	return s.updateAtContext(ctx, revision, func(r *resourceRecord) error {
		if !authorized(*r, actor, "tokens", "write", "*") {
			return ErrPermission
		}
		r.Credentials = slices.DeleteFunc(r.Credentials, func(v AdminCredential) bool { return v.ID == id })
		return nil
	})
}

func (s *Store) authenticateAdmin(secret string) (AdminCredential, bool) {
	hash := sha256.Sum256([]byte(secret))
	digest := hex.EncodeToString(hash[:])
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, c := range s.record.Credentials {
		if subtle.ConstantTimeCompare([]byte(c.Digest), []byte(digest)) == 1 && time.Now().Before(c.Expires) && authorized(s.record, c.Identity, "panel", "read", "*") {
			return c, true
		}
	}
	return AdminCredential{}, false
}

func (s *Store) adminSessionValid(identity, credential string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return authorized(s.record, identity, "panel", "read", "*") && slices.ContainsFunc(s.record.Credentials, func(c AdminCredential) bool {
		return c.ID == credential && c.Identity == identity && time.Now().Before(c.Expires)
	})
}
