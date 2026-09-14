package desktopipc

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/tsukiyoz/resona/internal/audio"
	"github.com/tsukiyoz/resona/internal/client"
)

// The Rust model tests consume the same file. Comparing exact JSON keys here
// catches acronym casing drift that Go's case-insensitive decoder would miss.
func TestSharedNativeContractMatchesGoJSON(t *testing.T) {
	const icon = "icon-ref-1"
	value := struct {
		Workspace client.Workspace  `json:"workspace"`
		Voice     client.VoiceState `json:"voice"`
	}{Workspace: client.Workspace{
		Servers:       []client.ServerProfile{{Protocol: "resona-noise", ServerPublicKey: "abababababababababababababababababababababababababababababababab", ID: "server-1", Name: "Contract", Address: "localhost:9988", Nickname: "Tester", SkipPasswordStorage: true}},
		Session:       client.Session{ID: "session-1", SendingMessageID: "message-1", Mode: "connected", ChannelID: "channel-1", Nickname: "Tester", ServerID: "server-1", ServerName: "Contract", IdentityUID: "identity-1", SelfID: "user-1", SwitchingChannelID: "channel-2", MemberSyncState: "ready"},
		Channels:      []client.Channel{{Kind: "channel", Align: "left", IconID: "1001", IconRef: icon, ID: "channel-1", Name: "Voice", Description: "Contract channel", Members: 1, ParentID: "parent-1", Order: "0"}},
		Users:         []client.User{{ID: "user-1", Instance: "member-1", PlaybackVolume: 130, PlaybackMuted: true, Nickname: "Tester", ChannelID: "channel-1", Self: true}},
		Messages:      []client.Message{{AuthorID: "user-1", Status: "sending", ID: "message-1", ChannelID: "channel-1", Author: "Tester", Text: "Contract message", CreatedAt: "2026-09-08T00:00:00Z"}},
		Notifications: []client.Notification{{ID: "notification-1", Kind: "connected", ChannelID: "channel-1", CreatedAt: "2026-09-08T00:00:00Z"}},
	}, Voice: client.VoiceState{VoiceConfig: audio.VoiceConfig{Enabled: true, Muted: true, InputDeviceID: "input-1", OutputDeviceID: "output-1", Volume: 75, InputGain: 150}, Active: true, ChannelCodec: audio.CodecOpusVoice, SpeakingClientIDs: []string{"42"}, LocalSpeaking: false}}
	actual, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := os.ReadFile("testdata/contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var got, want any
	if err := json.Unmarshal(actual, &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(expected, &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("native contract fixture differs from Go wire fields; update both consumer tests with an intentional protocol change")
	}
}
