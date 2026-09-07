package ts3

import (
	"strings"
	"testing"

	teamspeak "github.com/honeybbq/teamspeak-go"
	"github.com/tsukiyoz/resona/internal/client"
)

func TestChannelTextFollowsOrderedOwnChannelAndPreservesPlainText(t *testing.T) {
	s := newMoveTestConnection(t, nil)
	text := "  [b]literal[/b] <img src=x>\\s | 中文\nnext line  "
	params := map[string]string{"targetmode": "2", "invokerid": "8", "invokername": `Name\s`, "msg": text}
	if !s.state.apply(teamspeak.IncomingCommand{Name: "notifytextmessage", Params: params}) {
		t.Fatal("valid channel text was ignored")
	}
	first := s.state.snapshot()
	if len(first.Messages) != 1 || first.Messages[0].ChannelID != "1" || first.Messages[0].UserID != "8" || first.Messages[0].Author != `Name\s` || first.Messages[0].Text != text {
		t.Fatalf("channel text was transformed or attributed incorrectly: %+v", first.Messages)
	}
	first.Messages[0].Text = "mutated"
	if s.state.snapshot().Messages[0].Text != text {
		t.Fatal("snapshot shares message backing array")
	}
	s.state.apply(teamspeak.IncomingCommand{Name: "notifyclientmoved", Params: map[string]string{"clid": "7", "ctid": "2"}})
	if len(s.state.snapshot().Messages) != 0 {
		t.Fatal("next command repeated the previous transient message")
	}
	s.state.apply(teamspeak.IncomingCommand{Name: "notifytextmessage", Params: params})
	if got := s.state.snapshot().Messages; len(got) != 1 || got[0].ChannelID != "2" {
		t.Fatalf("message after own move did not follow new channel: %+v", got)
	}
	// Identical messages from a peer are separate real messages, not duplicates.
	if !s.state.apply(teamspeak.IncomingCommand{Name: "notifytextmessage", Params: params}) || len(s.state.snapshot().Messages) != 1 {
		t.Fatal("identical consecutive remote messages were collapsed")
	}
}

func TestChannelTextRejectsOtherScopesAndMalformedInput(t *testing.T) {
	for _, tc := range []struct {
		name, key, value string
	}{
		{"private", "targetmode", "1"},
		{"broadcast", "targetmode", "3"},
		{"unknown scope", "targetmode", ""},
		{"different channel", "target", "2"},
		{"invalid channel", "target", "invalid"},
		{"self echo", "invokerid", "7"},
		{"invalid sender", "invokerid", "0"},
		{"empty", "msg", ""},
		{"invalid utf8", "msg", string([]byte{0xff})},
		{"null", "msg", "part\x00rest"},
		{"oversized", "msg", strings.Repeat("x", client.MaxChannelMessageBytes+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newMoveTestConnection(t, nil)
			params := map[string]string{"targetmode": "2", "invokerid": "8", "invokername": "Peer", "msg": "message"}
			params[tc.key] = tc.value
			if s.state.applyChannelText(params) || len(s.state.snapshot().Messages) != 0 {
				t.Fatal("unexpected text reached the channel view")
			}
		})
	}
}

func TestChannelTextZeroTargetAndMissingAuthor(t *testing.T) {
	s := newMoveTestConnection(t, nil)
	s.state.users["8"] = client.User{ID: "8", Nickname: "Known member", ChannelID: "1"}
	params := map[string]string{"targetmode": "2", "target": "0", "invokerid": "8", "msg": "text"}
	if !s.state.applyChannelText(params) || s.state.state.Messages[0].Author != "Known member" {
		t.Fatal("zero target or cached author fallback failed")
	}
	delete(s.state.users, "8")
	if !s.state.applyChannelText(params) || s.state.state.Messages[0].Author != "用户 8" {
		t.Fatal("unknown author fallback failed")
	}
	s.state.listed = false
	if s.state.applyChannelText(params) {
		t.Fatal("incomplete login accepted a channel message")
	}
}
