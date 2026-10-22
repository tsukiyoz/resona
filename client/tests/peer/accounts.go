package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	w "github.com/tsukiyoz/resona/internal/nativewire"
	"github.com/tsukiyoz/resona/internal/noiseudp"
)

// A scripted fault peer isolates recovery ordering from account database tests.
func accountRecovery() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _, _ = io.Copy(io.Discard, os.Stdin); cancel() }()
	key, err := noiseudp.GenerateKey()
	if err != nil {
		return err
	}
	pub, _ := noiseudp.PublicKey(key)
	l, err := w.ListenNoise("127.0.0.1:0", key, 4)
	if err != nil {
		return err
	}
	defer l.Close()
	if err := announce(l.Addr().String(), pub); err != nil {
		return err
	}
	token := "fixture-token-0"
	for attempt := 0; ; attempt++ {
		c, err := l.Accept(ctx)
		if err != nil {
			return nil
		}
		func() {
			defer c.CloseWithError(0, "fixture interruption")
			s, err := c.AcceptStream(ctx)
			if err != nil {
				return
			}
			_ = s.SetDeadline(time.Now().Add(15 * time.Second))
			f, err := w.Read(s)
			var hello w.Hello
			if err != nil || w.Decode(f, &hello) != nil || w.VerifyHello(c, hello) != nil {
				return
			}
			channel := uint16(1)
			if attempt == 0 {
				channel = 2
			}
			state := w.State{Self: 1, Epoch: 1, IdentityUID: fmt.Sprintf("%x", sha256.Sum256(hello.PublicKey)), ServerRole: "guest",
				Channels: []w.Channel{{ID: 1, Name: "Lobby"}, {ID: 2, Name: "Gaming"}},
				Members:  []w.Member{{ID: 1, Channel: channel, Nickname: hello.Nickname, Instance: "fixture", Epoch: 1}}}
			if w.Write(s, w.WelcomeKind, 0, state) != nil {
				return
			}
			for {
				f, err := w.Read(s)
				if err != nil {
					return
				}
				reply := w.Reply{}
				switch f.Kind {
				case w.AccountKind:
					var command w.AccountCommand
					if w.Decode(f, &command) != nil {
						return
					}
					if attempt > 0 && command.Token != token {
						reply.Code = 10
					} else {
						token = fmt.Sprintf("fixture-token-%d", attempt+1)
						reply.AccountToken = token
						state.AccountUID, state.AccountName, state.ServerRole = strings.Repeat("a", 64), "alice", "member"
						state.Members[0].AccountUID = state.AccountUID
					}
				case w.MoveKind:
					if attempt == 1 {
						return // Authentication succeeded, but restoration must retry with its new token.
					}
					state.Members[0].Channel = 2
					state.Epoch++
					state.Members[0].Epoch = state.Epoch
				case w.VoiceStateKind:
					if attempt == 0 {
						return
					}
				}
				if w.Write(s, w.StateKind, 0, state) != nil || w.Write(s, w.ReplyKind, f.Request, reply) != nil {
					return
				}
			}
		}()
	}
}
