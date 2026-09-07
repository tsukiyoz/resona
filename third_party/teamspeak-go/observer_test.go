package teamspeak

import (
	"io"
	"log/slog"
	"reflect"
	"testing"

	"github.com/honeybbq/teamspeak-go/commands"
	"github.com/honeybbq/teamspeak-go/crypto"
)

func TestCommandObserverOrderAndIsolation(t *testing.T) {
	id, err := crypto.GenerateIdentity(0)
	if err != nil {
		t.Fatal(err)
	}
	var observed []IncomingCommand
	c := NewClient(id, "127.0.0.1:9987", "test",
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
		WithCommandObserver(nil),
		WithCommandObserver(func(command IncomingCommand) { command.Params["channel_name"] = "changed" }),
		WithCommandObserver(func(command IncomingCommand) { observed = append(observed, command) }))
	rc, result := c.cmdTrack.register()
	defer c.cmdTrack.unregister(rc)
	c.handleCommandLines("channellist cid=1 channel_name=Two\\sWords|cid=2 channel_name=Other\nchannellistfinished\x00")
	names := []string{}
	for _, command := range observed {
		names = append(names, command.Name)
	}
	if !reflect.DeepEqual(names, []string{"channellist", "channellist", "channellistfinished"}) {
		t.Fatalf("wrong order: %v", names)
	}
	if observed[0].Params["channel_name"] != "Two Words" || observed[1].Params["cid"] != "2" {
		t.Fatalf("observer mutated another observer: %+v", observed)
	}
	observed[0].Params["channel_name"] = "retained map mutation"
	c.cmdTrack.resolve(rc, nil)
	got := <-result
	if got.Data[0]["channel_name"] != "Two Words" {
		t.Fatal("observer mutated normal command dispatch")
	}
}

func TestSimilarNicknameCannotReplaceAuthenticatedSelf(t *testing.T) {
	id, err := crypto.GenerateIdentity(0)
	if err != nil {
		t.Fatal(err)
	}
	c := NewClient(id, "127.0.0.1:9987", "Resona",
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	c.clid = 7
	c.handler.SetClientID(7)
	c.handleClientEnterView(commands.ParseCommand("notifycliententerview clid=8 ctid=1 client_nickname=Resona123"))
	if c.clid != 7 {
		t.Fatalf("other user replaced authenticated self ID: %d", c.clid)
	}
}
