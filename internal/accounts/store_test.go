package accounts

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const testPassword = "correct horse battery staple"

func TestEightBytePasswordAcrossAccountActions(t *testing.T) {
	ctx := context.Background()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.Register(ctx, "alice", "short-7", "Alice"); !errors.Is(err, ErrInvalid) {
		t.Fatal("seven-byte registration password accepted", err)
	}
	a, err := s.Register(ctx, "alice", "exactly8", "Alice")
	if err != nil {
		t.Fatal("eight-byte registration password rejected", err)
	}
	if _, err := s.Password(ctx, "alice", "exactly8"); err != nil {
		t.Fatal("eight-byte password login failed", err)
	}
	if _, err := s.ChangePassword(ctx, a, "exactly8", "short-7"); !errors.Is(err, ErrInvalid) {
		t.Fatal("seven-byte replacement password accepted", err)
	}
	if _, err := s.ChangePassword(ctx, a, "exactly8", "changed8"); err != nil {
		t.Fatal("eight-byte replacement password rejected", err)
	}
	if _, err := s.ResetPassword(ctx, "alice", "short-7"); !errors.Is(err, ErrInvalid) {
		t.Fatal("seven-byte reset password accepted", err)
	}
	if _, err := s.ResetPassword(ctx, "alice", "reset-p8"); err != nil {
		t.Fatal("eight-byte reset password rejected", err)
	}
	if _, err := s.Password(ctx, "alice", "reset-p8"); err != nil {
		t.Fatal("reset password login failed", err)
	}
}

func TestAccountsPasswordTokenRestartAndRevocation(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	a, err := s.Register(ctx, "Alice", testPassword, "device-one")
	if err != nil {
		t.Fatal(err)
	}
	if a.Username != "alice" || len(a.UID) != 64 {
		t.Fatal("registration did not normalize the name and create a stable UID")
	}
	if _, err := s.Register(ctx, "ALICE", testPassword, "device-two"); !errors.Is(err, ErrExists) {
		t.Fatal("case variants bypassed the unique name constraint", err)
	}
	for _, name := range []string{"alice", "missing-user"} {
		if _, err := s.Password(ctx, name, "wrong password"); !errors.Is(err, ErrCredentials) {
			t.Fatal("wrong credentials were not rejected uniformly", err)
		}
	}
	verified, err := s.Password(ctx, "ALICE", testPassword)
	if err != nil || verified.UID != a.UID {
		t.Fatal("password login changed the account UID", err)
	}
	proof, token, err := s.Issue(ctx, verified)
	if err != nil || !s.Valid(ctx, proof) {
		t.Fatal("issued token is invalid", err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := s.Token(ctx, token)
	if err != nil || restored.UID != a.UID {
		t.Fatal("account token did not survive restart", err)
	}
	changed, err := s.ChangePassword(ctx, verified, testPassword, "a brand new long password")
	if err != nil || changed.Version != a.Version+1 {
		t.Fatal("password change failed", err)
	}
	if s.Valid(ctx, proof) {
		t.Fatal("password change left an old session proof valid")
	}
	if _, err := s.Token(ctx, token); !errors.Is(err, ErrCredentials) {
		t.Fatal("password change left a token valid", err)
	}
	if _, _, err := s.Issue(ctx, verified); !errors.Is(err, ErrCredentials) {
		t.Fatal("stale password verification minted a fresh token", err)
	}
	if _, err := s.Password(ctx, "alice", testPassword); !errors.Is(err, ErrCredentials) {
		t.Fatal("old password still works", err)
	}
	if _, err := s.Password(ctx, "alice", "a brand new long password"); err != nil {
		t.Fatal(err)
	}
	// Windows has no POSIX permission bits: os.Stat reports a fixed mode and the
	// file is protected by its ACL, so the mode check only means something on
	// Unix.
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(dir, "accounts.sqlite"))
		if err != nil || info.Mode().Perm()&0o077 != 0 {
			t.Fatal("account database permissions are too broad", err)
		}
	}
}

func TestSQLiteURIDoesNotParseThePathAsAuthority(t *testing.T) {
	// A drive letter read as a URI authority is what made Windows reject every
	// database with "invalid uri authority: C:".
	for _, path := range []string{
		filepath.Join(t.TempDir(), "accounts.sqlite"),
		filepath.Join(string(filepath.Separator), "srv", "resona", "accounts.sqlite"),
	} {
		uri := sqliteURI(path)
		parsed, err := url.Parse(uri)
		if err != nil {
			t.Fatalf("%q -> %q: %v", path, uri, err)
		}
		if parsed.Host != "" {
			t.Fatalf("%q -> %q parses %q as the URI authority", path, uri, parsed.Host)
		}
		if strings.Contains(uri, `\`) {
			t.Fatalf("%q -> %q keeps backslashes in a URI", path, uri)
		}
	}
}

func TestAccountUIDsAndPasswordSaltsAreIndependent(t *testing.T) {
	ctx := context.Background()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	owner, err := s.Register(ctx, "owner", testPassword, "玩家")
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Register(ctx, "second", testPassword, "玩家")
	if err != nil || second.UID == owner.UID {
		t.Fatal("accounts did not receive independent stable UIDs", err)
	}
	updated, err := s.UpdateProfile(ctx, owner, "新昵称")
	if err != nil || updated.UID != owner.UID || updated.Nickname != "新昵称" {
		t.Fatal("profile update changed identity", err)
	}
	var hashes []string
	for _, uid := range []string{owner.UID, second.UID} {
		var hash string
		if err := s.db.QueryRowContext(ctx, "SELECT password_hash FROM accounts WHERE uid=?", uid).Scan(&hash); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(hash, testPassword) || !strings.HasPrefix(hash, "$argon2id$") {
			t.Fatal("password was not stored as an Argon2id PHC string")
		}
		hashes = append(hashes, hash)
	}
	if hashes[0] == hashes[1] {
		t.Fatal("equal passwords reused their salt")
	}
}

func TestAccountBoundsAndCancelledWork(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Register(ctx, "alice", testPassword, "device"); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled registration persisted an account", err)
	}
	for _, name := range []string{"ab", "a b", "../alice", strings.Repeat("a", 33)} {
		if _, err := s.Register(context.Background(), name, testPassword, "device"); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid account name accepted", err)
		}
	}
	s.hash <- struct{}{}
	s.hash <- struct{}{}
	if _, err := s.Password(context.Background(), "alice", testPassword); !errors.Is(err, ErrBusy) {
		t.Fatal("hashing concurrency is not bounded", err)
	}
}
