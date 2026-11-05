package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukiyoz/resona/internal/server"
)

func TestOfflinePasswordCommandsDoNotSavePlaintextFiles(t *testing.T) {
	dir := t.TempDir()
	cmd, err := newCommand()
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetIn(strings.NewReader("explicit-private-password\n"))
	cmd.SetArgs([]string{"password", "set", "--password-stdin", "--access-dir", dir})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "explicit-private-password") {
		t.Fatal("set echoed plaintext")
	}
	reset, err := executeTest(t, "password", "reset", "--access-dir", dir)
	if err != nil || len(strings.TrimSpace(reset)) < 26 || strings.Count(reset, "\n") != 1 {
		t.Fatal("reset must display one secret after saving", err)
	}
	for _, name := range []string{"owner-claim", "server-password"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatal("plaintext file created", name, err)
		}
	}
	lock, err := server.LockAccess(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if out, err := executeTest(t, "password", "reset", "--access-dir", dir); err == nil || out != "" {
		t.Fatal("locked operation generated output or bypassed access lock")
	}
}

func TestOldPasswordStartupInputsAreRejected(t *testing.T) {
	if _, err := executeTest(t, "serve", "--password-file", "old-file"); err == nil {
		t.Fatal("old password flag accepted")
	}
	for _, env := range []string{"RESONA_SERVER_PASSWORD", "RESONA_SERVER_PASSWORD_FILE"} {
		t.Run(env, func(t *testing.T) {
			t.Setenv(env, "private-value-must-not-leak")
			out, err := executeTest(t, "serve")
			if err == nil || !strings.Contains(err.Error(), "password set") || strings.Contains(out+err.Error(), "private-value-must-not-leak") {
				t.Fatal("old startup input ignored or leaked")
			}
		})
	}
}
