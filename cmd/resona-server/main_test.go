package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	w "github.com/tsukiyoz/resona/internal/nativewire"

	"github.com/tsukiyoz/resona/internal/server"
)

func executeTest(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd, err := newCommand()
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return out.String(), err
}

func TestInvalidVoiceQueueSlotsDoNotReadKeysOrCreateData(t *testing.T) {
	dir := t.TempDir()
	for _, slots := range []int{-1, 0, server.MaxVoiceQueueSlots + 1} {
		_, err := executeTest(t, "serve", "--voice-queue-slots", strconv.Itoa(slots),
			"--noise-key", filepath.Join(dir, "missing.key"), "--access-dir", filepath.Join(dir, "access"))
		if err == nil || !strings.Contains(err.Error(), "--voice-queue-slots") {
			t.Fatalf("invalid capacity did not fail before file access: %v", err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("invalid queue configuration created files: %v", err)
	}
}

func TestOfflineAdminCredentialBindsExistingOwnerAndUsesAccessLock(t *testing.T) {
	dir := t.TempDir()
	store, err := server.OpenStore(server.ChatConfig{Directory: filepath.Join(dir, "chat")}, dir, []w.Channel{{ID: 1, Name: "Lobby"}})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := store.ProvisionOwnerClaim(false)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Claim(strings.Repeat("a", 64), claim); err != nil {
		t.Fatal(err)
	}
	store.Close()
	out, err := executeTest(t, "admin", "token", "--access-dir", dir, "--label", "Laptop", "--expires", "24h")
	if err != nil || len(strings.TrimSpace(out)) < 32 {
		t.Fatal("credential provisioning failed", err)
	}
	store, err = server.OpenStore(server.ChatConfig{Directory: filepath.Join(dir, "chat")}, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, _, err = store.IssueAdminCredential(context.Background(), strings.Repeat("b", 64), strings.Repeat("a", 64), "Invalid", time.Hour, 0); err == nil {
		t.Fatal("member issued token")
	}
	lock, err := server.LockAccess(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if _, err = executeTest(t, "admin", "token", "--access-dir", dir); err == nil {
		t.Fatal("live store lock bypassed")
	}
}

func TestCommandHelpVersionAndInvalidInputDoNotStartServer(t *testing.T) {
	for _, args := range [][]string{nil, {"--help"}, {"serve", "--help"}, {"owner", "--help"}, {"version"}, {"--version"}} {
		out, err := executeTest(t, args...)
		if err != nil || !strings.Contains(out, "resona-server") {
			t.Fatalf("%v: %s %v", args, out, err)
		}
	}
	for _, args := range [][]string{{"unknown"}, {"serve", "extra"}, {"--no-such-flag"}, {"--init-key"}, {"--init-owner"}, {"--reset-owner-claim"}, {"--listen", "127.0.0.1:0"}, {"identity", "init", "--init-owner"}, {"identity", "init", "--listen", "127.0.0.1:0"}, {"admin", "token", "--noise-key", "unused"}} {
		if _, err := executeTest(t, args...); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

func TestIdentityInitNeverOverwrites(t *testing.T) {
	for _, args := range [][]string{{"identity", "init"}} {
		path := filepath.Join(t.TempDir(), "noise.key")
		args = append(args, "--noise-key", path)
		out, err := executeTest(t, args...)
		if err != nil || !strings.Contains(out, "X25519") {
			t.Fatalf("init: %s %v", out, err)
		}
		before, _ := os.ReadFile(path)
		if len(before) != 32 {
			t.Fatal("invalid identity")
		}
		if _, err := executeTest(t, args...); err == nil {
			t.Fatal("identity overwritten")
		}
		after, _ := os.ReadFile(path)
		if !bytes.Equal(before, after) {
			t.Fatal("key modified")
		}
	}
}

func TestOfflineOwnerCommandsUseSameStoreAndLock(t *testing.T) {
	dir := t.TempDir()
	old, err := executeTest(t, "owner", "init", "--access-dir", dir)
	if err != nil || len(strings.TrimSpace(old)) != 64 {
		t.Fatalf("owner init: %v", err)
	}
	if _, err = executeTest(t, "owner", "init", "--access-dir", dir); err == nil {
		t.Fatal("existing claim overwritten")
	}
	fresh, err := executeTest(t, "owner", "reset-claim", "--access-dir", dir)
	if err != nil || fresh == old {
		t.Fatalf("reset: %v", err)
	}
	lock, err := server.LockAccess(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if _, err = executeTest(t, "owner", "reset-claim", "--access-dir", dir); err == nil {
		t.Fatal("live access lock bypassed")
	}
}

func TestReadOnlyCommandsAndInvalidRecoveryHaveNoStoreSideEffects(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
	t.Setenv("AppData", dir)
	for _, args := range [][]string{nil, {"version"}, {"serve", "--help"}, {"account", "reset-password", "--username", "alice", "--access-dir", filepath.Join(dir, "access")}} {
		_, _ = executeTest(t, args...)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("non-executing commands created server data: %v %v", entries, err)
	}
}
