package native

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tsukiyoz/resona/internal/client"
	w "github.com/tsukiyoz/resona/internal/nativewire"
	"github.com/tsukiyoz/resona/internal/server"
)

func TestOwnerClaimAndPersistentIdentityAcrossServerRestart(t *testing.T) {
	for _, noise := range []bool{false, true} {
		name := "quic"
		if noise {
			name = "noise"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			token, err := server.InitOwnerClaim(dir)
			if err != nil {
				t.Fatal(err)
			}
			owner, err := server.OpenOwnership(dir)
			if err != nil {
				t.Fatal(err)
			}
			p, stop, done := startServerOwned(t, owner, noise)
			connector := Connector{IdentityPath: filepath.Join(t.TempDir(), "identity.key")}
			connect := func(p client.ServerProfile) (*connection, *observer) {
				t.Helper()
				o := &observer{}
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				raw, err := connector.Connect(ctx, p, "test-password", o.update)
				if err != nil {
					t.Fatal(err)
				}
				c := raw.(*connection)
				t.Cleanup(func() { _ = c.Close() })
				return c, o
			}
			a, oa := connect(p)
			uid := oa.snapshot().IdentityUID
			if len(uid) != 64 || oa.snapshot().ServerRole != "member" || !oa.snapshot().CanClaimOwner {
				t.Fatal("incorrect initial identity state")
			}
			if a.ClaimOwner(context.Background(), strings.Repeat("0", 64)) == nil {
				t.Fatal("wrong code accepted")
			}
			if err := a.ClaimOwner(context.Background(), token); err != nil {
				t.Fatal(err)
			}
			eventually(t, func() bool { return oa.snapshot().ServerRole == "owner" && !oa.snapshot().CanClaimOwner })
			b, ob := connectTest(t, p)
			if ob.snapshot().IdentityUID == uid || ob.snapshot().ServerRole != "member" || ob.snapshot().CanClaimOwner {
				t.Fatal("nickname inherited ownership")
			}
			if b.ClaimOwner(context.Background(), token) == nil {
				t.Fatal("consumed code accepted")
			}
			_ = a.Close()
			_ = b.Close()
			stop()
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			owner, err = server.OpenOwnership(dir)
			if err != nil {
				t.Fatal(err)
			}
			p, _, _ = startServerOwned(t, owner, noise)
			_, again := connect(p)
			if again.snapshot().IdentityUID != uid || again.snapshot().ServerRole != "owner" || again.snapshot().CanClaimOwner {
				t.Fatal("restart lost ownership")
			}
		})
	}
}

func TestNativeIdentityRejectsMissingForgedAndCrossConnectionProof(t *testing.T) {
	for _, noise := range []bool{false, true} {
		name := "quic"
		if noise {
			name = "noise"
		}
		t.Run(name, func(t *testing.T) {
			p, _, _ := startServer(t, noise)
			dial := func() w.Connection {
				t.Helper()
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				var c w.Connection
				var err error
				if noise {
					key, _ := hex.DecodeString(p.ServerPublicKey)
					c, err = w.DialNoise(ctx, p.Address, key)
				} else {
					cfg, _ := w.ClientTLS(p.CertificateFingerprint)
					c, err = w.DialQUIC(ctx, p.Address, cfg)
				}
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = c.CloseWithError(0, "done") })
				return c
			}
			_, key, _ := ed25519.GenerateKey(rand.Reader)
			source := dial()
			recorded := w.Hello{Nickname: p.Nickname, Password: "test-password"}
			if err := w.SignHello(source, key, &recorded); err != nil {
				t.Fatal(err)
			}
			_ = source.CloseWithError(0, "done")
			for _, mode := range []string{"missing", "forged", "replay"} {
				c := dial()
				h := w.Hello{Nickname: p.Nickname, Password: "test-password"}
				if mode == "forged" {
					if err := w.SignHello(c, key, &h); err != nil {
						t.Fatal(err)
					}
					h.Signature[0] ^= 1
				}
				if mode == "replay" {
					h = recorded
				}
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				stream, err := c.OpenStreamSync(ctx)
				cancel()
				if err != nil {
					t.Fatal(err)
				}
				_ = stream.SetDeadline(time.Now().Add(3 * time.Second))
				if err := w.Write(stream, w.HelloKind, 0, h); err != nil {
					t.Fatal(err)
				}
				if f, err := w.Read(stream); err == nil && f.Kind == w.WelcomeKind {
					t.Fatalf("%s accepted", mode)
				}
				_ = c.CloseWithError(0, "done")
			}
		})
	}
}
