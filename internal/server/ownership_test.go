package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestResetClaimReplacesExpiredAndUnusedCodesButNeverOwner(t *testing.T) {
	s := testStore(t, t.TempDir())
	old, err := s.ProvisionOwnerClaim(false)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.update(func(r *resourceRecord) error { r.Claim.Expires = time.Now().Add(-time.Hour); return nil }); err != nil {
		t.Fatal(err)
	}
	if s.Claim(strings.Repeat("a", 64), old) == nil {
		t.Fatal("expired code accepted")
	}
	fresh, err := s.ProvisionOwnerClaim(true)
	if err != nil || fresh == old {
		t.Fatalf("renew: %v", err)
	}
	newer, err := s.ProvisionOwnerClaim(true)
	if err != nil || newer == fresh {
		t.Fatalf("replace unexpired: %v", err)
	}
	id := strings.Repeat("a", 64)
	for _, token := range []string{old, fresh} {
		if s.Claim(id, token) == nil {
			t.Fatal("old token accepted")
		}
	}
	if expires := time.Until(s.snapshot().Claim.Expires); expires < 23*time.Hour || expires > 24*time.Hour {
		t.Fatal("incorrect expiry")
	}
	if err = s.Claim(id, newer); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ProvisionOwnerClaim(true); err == nil {
		t.Fatal("owner reset accepted")
	}
	if s.snapshot().Owner != id || s.snapshot().Claim.Digest != "" {
		t.Fatal("owner/claim transaction broken")
	}
}

func TestAccessLockReleasedOnClose(t *testing.T) {
	dir := t.TempDir()
	a, err := LockAccess(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if b, err := LockAccess(dir); err == nil {
		b.Close()
		t.Fatal("concurrent access allowed")
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	b, err := LockAccess(dir)
	if err != nil {
		t.Fatal(err)
	}
	b.Close()
}

func TestOwnerClaimPersistsAndCannotReplay(t *testing.T) {
	dir := t.TempDir()
	a := testStore(t, dir)
	token, err := a.ProvisionOwnerClaim(false)
	if err != nil {
		t.Fatal(err)
	}
	// Independent caches and writer locks exercise server-side CAS, not just a Go mutex.
	b := &Store{chat: a.chat, kv: a.kv, record: a.snapshot(), revision: a.revision, updateGate: make(chan struct{}, 1)}
	if a.Claim(strings.Repeat("1", 64), strings.Repeat("f", 64)) == nil {
		t.Fatal("wrong token accepted")
	}
	var winners atomic.Int32
	var wg sync.WaitGroup
	for i, s := range []*Store{a, b} {
		wg.Go(func() {
			if s.Claim(strings.Repeat(string(rune('1'+i)), 64), token) == nil {
				winners.Add(1)
			}
		})
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatalf("winners=%d", winners.Load())
	}
	a.Close()
	reloaded := testStore(t, dir)
	if role, claim := reloaded.Status(reloaded.snapshot().Owner); role != "owner" || claim {
		t.Fatal("owner not restored")
	}
	if reloaded.Claim(strings.Repeat("3", 64), token) == nil {
		t.Fatal("replay accepted")
	}
	if _, err := reloaded.ProvisionOwnerClaim(false); err == nil {
		t.Fatal("claimed server reinitialized")
	}
	data, _ := json.Marshal(reloaded.snapshot())
	if strings.Contains(string(data), token) {
		t.Fatal("plaintext token persisted")
	}
}

func TestOwnerClaimDisabledAndPersistenceFailureDoesNotGrant(t *testing.T) {
	s := testStore(t, t.TempDir())
	id := strings.Repeat("a", 64)
	if _, ok := s.Status(id); ok {
		t.Fatal("unprovisioned claim enabled")
	}
	token, err := s.ProvisionOwnerClaim(false)
	if err != nil {
		t.Fatal(err)
	}
	s.chat.conn.Close()
	if s.Claim(id, token) == nil {
		t.Fatal("failed publication accepted")
	}
	if role, _ := s.Status(id); role == "owner" {
		t.Fatal("failed publication granted owner")
	}
}

func TestOldDeviceOwnerAndClaimsAreIgnored(t *testing.T) {
	dir := t.TempDir()
	id := strings.Repeat("a", 64)
	data := []byte(`{"version":1,"identity":"` + id + `"}`)
	if err := os.WriteFile(filepath.Join(dir, "owner.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "claim.json"), []byte("consumed"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := testStore(t, dir)
	if role, canClaim := s.Status(""); role != "guest" || canClaim || s.OwnerIdentity() != "" {
		t.Fatal("old device privilege inherited")
	}
	s.Close()
	if err := os.WriteFile(filepath.Join(dir, "owner.json"), []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	reopened := testStore(t, dir)
	if role, _ := reopened.Status(""); role != "guest" || reopened.OwnerIdentity() != "" {
		t.Fatal("stale legacy file overwrote KV")
	}
}
