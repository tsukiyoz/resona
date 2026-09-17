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
	dir := t.TempDir()
	old, err := InitOwnerClaim(dir)
	if err != nil {
		t.Fatal(err)
	}
	s, err := OpenOwnership(dir)
	if err != nil {
		t.Fatal(err)
	}
	s.claim.Expires = time.Now().Add(-time.Hour)
	data, _ := json.Marshal(s.claim)
	if err = os.WriteFile(filepath.Join(dir, "claim.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	fresh, err := ResetOwnerClaim(dir)
	if err != nil || fresh == old {
		t.Fatalf("renew: %v", err)
	}
	newer, err := ResetOwnerClaim(dir)
	if err != nil || newer == fresh {
		t.Fatalf("replace unexpired: %v", err)
	}
	s, err = OpenOwnership(dir)
	if err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("a", 64)
	for _, token := range []string{old, fresh} {
		if s.Claim(id, token) == nil {
			t.Fatal("old token accepted")
		}
	}
	if expires := time.Until(s.claim.Expires); expires < 23*time.Hour || expires > 24*time.Hour {
		t.Fatal("incorrect expiry")
	}
	if err = s.Claim(id, newer); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(dir, "owner.json"))
	if _, err = ResetOwnerClaim(dir); err == nil {
		t.Fatal("owner reset accepted")
	}
	after, _ := os.ReadFile(filepath.Join(dir, "owner.json"))
	if string(before) != string(after) {
		t.Fatal("owner modified")
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
	token, err := InitOwnerClaim(dir)
	if err != nil {
		t.Fatal(err)
	}
	a, err := OpenOwnership(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := OpenOwnership(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Claim(strings.Repeat("1", 64), strings.Repeat("f", 64)); err == nil {
		t.Fatal("wrong token accepted")
	}
	var winners atomic.Int32
	var wg sync.WaitGroup
	for i, s := range []*Ownership{a, b} {
		wg.Add(1)
		go func(i int, s *Ownership) {
			defer wg.Done()
			if s.Claim(strings.Repeat(string(rune('1'+i)), 64), token) == nil {
				winners.Add(1)
			}
		}(i, s)
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatalf("winners=%d", winners.Load())
	}
	reloaded, err := OpenOwnership(dir)
	if err != nil {
		t.Fatal(err)
	}
	if role, claim := reloaded.Status(reloaded.owner); role != "owner" || claim {
		t.Fatal("owner not restored")
	}
	if err := reloaded.Claim(strings.Repeat("3", 64), token); err == nil {
		t.Fatal("replay accepted")
	}
	if _, err := InitOwnerClaim(dir); err == nil {
		t.Fatal("claimed server reinitialized")
	}
	data, _ := os.ReadFile(filepath.Join(dir, "claim.json"))
	if strings.Contains(string(data), token) {
		t.Fatal("plaintext token persisted")
	}
}

func TestOwnerClaimDisabledExpiredAndCorrupt(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenOwnership(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Status(strings.Repeat("1", 64)); ok {
		t.Fatal("unprovisioned claim enabled")
	}
	token, err := InitOwnerClaim(dir)
	if err != nil {
		t.Fatal(err)
	}
	s, err = OpenOwnership(dir)
	if err != nil {
		t.Fatal(err)
	}
	s.claim.Expires = time.Now().Add(-time.Second)
	if err := s.Claim(strings.Repeat("1", 64), token); err == nil {
		t.Fatal("expired token accepted")
	}
	if err := os.WriteFile(filepath.Join(dir, "owner.json"), []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenOwnership(dir); err == nil {
		t.Fatal("corrupt owner accepted")
	}
}

func TestOwnerClaimPersistenceFailureDoesNotGrant(t *testing.T) {
	dir := t.TempDir()
	token, err := InitOwnerClaim(dir)
	if err != nil {
		t.Fatal(err)
	}
	s, err := OpenOwnership(dir)
	if err != nil {
		t.Fatal(err)
	}
	// A directory at the destination deterministically prevents publication,
	// including when tests run with privileges that bypass file mode checks.
	if err := os.Mkdir(filepath.Join(dir, "owner.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("a", 64)
	if err := s.Claim(id, token); err == nil {
		t.Fatal("failed publication accepted")
	}
	if role, _ := s.Status(id); role == "owner" {
		t.Fatal("failed publication granted owner")
	}
}
