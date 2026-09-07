package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tsukiyoz/resona/internal/client"
	"github.com/tsukiyoz/resona/internal/config"
)

func TestInitializationRetriesAfterConfigRepairAndPreservesSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "servers.json")
	if err := os.WriteFile(path, []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	attempts := 0
	app := &App{newService: func() (*client.Service, error) {
		attempts++
		return client.New(config.New(path))
	}}
	if _, err := app.GetWorkspace(); err == nil {
		t.Fatal("invalid config must surface an initialization error")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "{broken" {
		t.Fatalf("failed initialization modified config: %q, %v", data, err)
	}
	if err := os.WriteFile(path, []byte("[]"), 0600); err != nil {
		t.Fatal(err)
	}
	state, err := app.GetWorkspace()
	if err != nil || state.Session.Mode != "offline" {
		t.Fatalf("retry did not recover after config repair: %+v, %v", state, err)
	}
	if _, err := app.OpenPreview(); err != nil {
		t.Fatal(err)
	}
	if _, err := app.SendMessage("hello"); err != nil {
		t.Fatal(err)
	}
	state, err = app.GetWorkspace()
	if err != nil || state.Session.Mode != "preview" || len(state.Messages) != 1 {
		t.Fatalf("successful service was not preserved: %+v, %v", state, err)
	}
	if attempts != 2 {
		t.Fatalf("initialized %d times, want one failure and one success", attempts)
	}
}
