package ts3

import (
	"context"
	"errors"
	"testing"
	"time"

	teamspeak "github.com/honeybbq/teamspeak-go"
	"github.com/honeybbq/teamspeak-go/commands"
	"github.com/tsukiyoz/resona/internal/audio"
	"github.com/tsukiyoz/resona/internal/client"
)

func voiceReducer(mode string, channel map[string]string) *reducer {
	r := newReducer("uid")
	r.apply(teamspeak.IncomingCommand{Name: "initserver", Params: map[string]string{"aclid": "7", "virtualserver_codec_encryption_mode": mode}})
	channel["cid"] = "10"
	r.apply(teamspeak.IncomingCommand{Name: "channellist", Params: channel})
	r.apply(teamspeak.IncomingCommand{Name: "channellistfinished"})
	r.apply(teamspeak.IncomingCommand{Name: "notifycliententerview", Params: map[string]string{"clid": "7", "ctid": "10"}})
	return r
}

func TestCurrentVoiceChannelEncryptionPrecedence(t *testing.T) {
	tests := []struct {
		name, mode, channelFlag string
		unencrypted             bool
	}{
		{"forced off", "1", "", true},
		{"forced on", "2", "", false},
		{"per-channel off", "0", "1", true},
		{"per-channel on", "0", "0", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			params := map[string]string{"channel_codec": "4"}
			if test.channelFlag != "" {
				params["channel_codec_is_unencrypted"] = test.channelFlag
			}
			r := voiceReducer(test.mode, params)
			voice, err := r.currentVoiceChannel()
			unencrypted, encryptionErr := r.voiceEncryption(voice)
			if err != nil || encryptionErr != nil || unencrypted != test.unencrypted {
				t.Fatalf("voice=%+v unencrypted=%v err=%v encryptionErr=%v", voice, unencrypted, err, encryptionErr)
			}
		})
	}
}

func TestCurrentVoiceChannelRejectsUnknownEncryptionAndCodec(t *testing.T) {
	r := voiceReducer("0", map[string]string{"channel_codec": "4"})
	voice, err := r.currentVoiceChannel()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.voiceEncryption(voice); err == nil {
		t.Fatal("unknown per-channel encryption accepted")
	}
	if _, err := voiceReducer("1", map[string]string{}).currentVoiceChannel(); err == nil {
		t.Fatal("missing codec accepted")
	}
}

func TestResolveVoiceChannelQueriesOwnChannelInfoOnce(t *testing.T) {
	lifetime, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &connection{state: voiceReducer("0", map[string]string{"channel_codec": "4"}), commandGate: make(chan struct{}, 1), lifetime: lifetime}
	queries := 0
	s.execCommandResponse = func(_ context.Context, raw string) ([]map[string]string, error) {
		queries++
		cmd := commands.ParseCommand(raw)
		if cmd == nil || cmd.Name != "channelinfo" || cmd.Params["cid"] != "10" {
			t.Fatalf("query = %q", raw)
		}
		return []map[string]string{{"channel_codec": "4", "channel_codec_is_unencrypted": "1"}}, nil
	}
	voice, err := s.resolveVoiceChannel(context.Background())
	if err != nil || !voice.encryptionKnown || !voice.unencrypted {
		t.Fatalf("voice=%+v err=%v", voice, err)
	}
	if _, err := s.resolveVoiceChannel(context.Background()); err != nil {
		t.Fatal(err)
	}
	if queries != 1 {
		t.Fatalf("channelinfo queries = %d", queries)
	}
}

func TestResolveVoiceChannelRetriesFloodResponseOnce(t *testing.T) {
	lifetime, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &connection{state: voiceReducer("0", map[string]string{"channel_codec": "4"}), commandGate: make(chan struct{}, 1), lifetime: lifetime, voiceQueryRetryDelay: time.Millisecond}
	queries := 0
	s.execCommandResponse = func(context.Context, string) ([]map[string]string, error) {
		queries++
		if queries == 1 {
			return nil, &teamspeak.CommandError{ID: 524, Message: "private flood text"}
		}
		return []map[string]string{{"channel_codec": "4", "channel_codec_is_unencrypted": "1"}}, nil
	}
	if _, err := s.resolveVoiceChannel(context.Background()); err != nil {
		t.Fatal(err)
	}
	if queries != 2 {
		t.Fatalf("channelinfo queries = %d", queries)
	}
}

func TestResolveVoiceChannelSanitizesQueryErrorWithCode(t *testing.T) {
	lifetime, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &connection{state: voiceReducer("0", map[string]string{"channel_codec": "4"}), commandGate: make(chan struct{}, 1), lifetime: lifetime}
	s.execCommandResponse = func(context.Context, string) ([]map[string]string, error) {
		return nil, &teamspeak.CommandError{ID: 2568, Message: "private server text"}
	}
	_, err := s.resolveVoiceChannel(context.Background())
	if err == nil || err.Error() != "无法读取当前频道的语音加密状态（错误码 2568）" {
		t.Fatalf("error = %v", err)
	}
	if errors.Is(err, context.Canceled) {
		t.Fatal("permission error became cancellation")
	}
}

func TestObservedVoiceCachesEncryptionFlag(t *testing.T) {
	s := &connection{state: voiceReducer("0", map[string]string{"channel_codec": "4"})}
	s.state.users["8"] = client.User{ID: "8", ChannelID: "10"}
	s.observeVoice(teamspeak.VoicePacket{SenderID: 8, Codec: 4, Encrypted: true, Data: []byte{1}})
	voice, err := s.state.currentVoiceChannel()
	if err != nil {
		t.Fatal(err)
	}
	if unencrypted, err := s.state.voiceEncryption(voice); err != nil || unencrypted {
		t.Fatalf("unencrypted=%v err=%v", unencrypted, err)
	}
}

func TestConnectionVoiceCodecAndMuteCommand(t *testing.T) {
	lifetime, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &connection{state: voiceReducer("1", map[string]string{"channel_codec": "5"}), commandGate: make(chan struct{}, 1), lifetime: lifetime}
	var raw string
	s.execCommand = func(_ context.Context, command string) error { raw = command; return nil }
	codec, err := s.VoiceCodec()
	if err != nil || codec != audio.CodecOpusMusic {
		t.Fatalf("codec=%v err=%v", codec, err)
	}
	if err := s.SetVoiceMuted(context.Background(), false, true); err != nil {
		t.Fatal(err)
	}
	cmd := commands.ParseCommand(raw)
	if cmd == nil || cmd.Params["client_input_muted"] != "0" || cmd.Params["client_output_muted"] != "1" {
		t.Fatalf("mute command = %q", raw)
	}
}

func TestConnectionVoiceHandlerStopsSynchronously(t *testing.T) {
	s := &connection{}
	var packets []audio.Packet
	s.SetVoiceHandler(func(packet audio.Packet) { packets = append(packets, packet) })
	s.observeVoice(teamspeak.VoicePacket{Codec: 4, Data: []byte{1}})
	s.observeVoice(teamspeak.VoicePacket{Sequence: 2, SenderID: 8, Codec: 4, End: true})
	s.SetVoiceHandler(nil)
	s.observeVoice(teamspeak.VoicePacket{Codec: 4, Data: []byte{1}})
	if len(packets) != 2 || !packets[1].End || packets[1].Sequence != 2 || packets[1].SenderID != 8 || len(packets[1].Data) != 0 {
		t.Fatalf("handler packets = %+v", packets)
	}
	s.observeVoice(teamspeak.VoicePacket{Whisper: true, Codec: 4, Data: []byte{1}})
	if len(packets) != 2 {
		t.Fatal("unexpected whisper dispatch")
	}
}
