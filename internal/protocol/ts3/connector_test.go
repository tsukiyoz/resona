package ts3

import (
	"context"
	"strings"
	"testing"

	"github.com/honeybbq/teamspeak-go/commands"
)

func TestMuteMiddlewarePreservesOtherFields(t *testing.T) {
	var raw string
	send := muteOutput(func(command string) error { raw = command; return nil })
	if err := send(`clientupdate client_input_muted=0 client_output_muted=0 client_nickname=two\swords`); err != nil {
		t.Fatal(err)
	}
	cmd := commands.ParseCommand(raw)
	if cmd == nil {
		t.Fatal("invalid command")
	}
	if cmd.Params["client_input_muted"] != "1" || cmd.Params["client_output_muted"] != "1" || cmd.Params["client_nickname"] != "two words" {
		t.Fatalf("bad clientupdate: %v", cmd.Params)
	}
	if err := send("clientdisconnect reasonmsg=Shutdown"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(raw, "clientdisconnect ") {
		t.Fatal("changed disconnect command")
	}
}

func TestResolveNumericEndpoint(t *testing.T) {
	for _, address := range []string{"127.0.0.1:9988", "[::1]:9988"} {
		got, err := resolveEndpoint(context.Background(), address)
		if err != nil || got != address {
			t.Fatalf("resolve %q = %q, %v", address, got, err)
		}
	}
}
