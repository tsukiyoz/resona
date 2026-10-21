// Local-only Go interoperability fixture for the Rust client transport.
package main

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"time"

	"github.com/flynn/noise"
	w "github.com/tsukiyoz/resona/internal/nativewire"
	"github.com/tsukiyoz/resona/internal/noiseudp"
	"github.com/tsukiyoz/resona/internal/server"
)

func main() {
	loss := flag.Bool("loss", false, "drop first handshake response and control ACK in both directions")
	blackhole := flag.Bool("blackhole-acks", false, "drop all server control ACKs")
	application := flag.Bool("server", false, "run the real application server")
	slow := flag.Bool("slow-command-acks", false, "drop the first two post-login control ACKs")
	vectors := flag.Bool("vectors", false, "print fixed-key Go rekey reference vectors")
	codec := flag.Bool("codec", false, "local Opus cross-language reference")
	flag.Parse()
	if *codec {
		if err := codecReference(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if *vectors {
		printVectors()
		return
	}
	if err := run(*loss, *blackhole, *application, *slow); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(loss, blackhole, application, slow bool) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	go func() { _, _ = io.Copy(io.Discard, os.Stdin); cancel() }()
	key, err := noiseudp.GenerateKey()
	if err != nil {
		return err
	}
	pub, _ := noiseudp.PublicKey(key)
	if application {
		dir, err := os.MkdirTemp("", "resona-interop-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(dir)
		store, err := server.OpenStore(server.ChatConfig{Directory: dir}, "", []w.Channel{{ID: 1, Name: "Lobby"}, {ID: 2, Name: "Gaming"}})
		if err != nil {
			return err
		}
		defer store.Close()
		claim, err := store.ProvisionOwnerClaim(false)
		if err != nil {
			return err
		}
		s, err := server.Listen("127.0.0.1:0", server.Config{Store: store, NoiseKey: key, Name: "Rust interop", Password: "local-test-only", MaxClients: 4})
		if err != nil {
			return err
		}
		address := s.Addr().String()
		if slow {
			proxy, err := startProxy(s.Addr().(*net.UDPAddr), false, false, true)
			if err != nil {
				return err
			}
			defer proxy.Close()
			address = proxy.LocalAddr().String()
		}
		if err := json.NewEncoder(os.Stdout).Encode(map[string]string{"address": address, "backend": s.Addr().String(), "public_key": hex.EncodeToString(pub), "claim": claim}); err != nil {
			cancel()
		}
		return s.Serve(ctx)
	}
	l, err := noiseudp.Listen("127.0.0.1:0", key, 4)
	if err != nil {
		return err
	}
	defer l.Close()
	address := l.Addr().String()
	if loss || blackhole {
		proxy, err := startProxy(l.Addr().(*net.UDPAddr), loss, blackhole, false)
		if err != nil {
			return err
		}
		defer proxy.Close()
		address = proxy.LocalAddr().String()
	}
	if err := announce(address, pub); err != nil {
		return err
	}
	// The fixture serves one client and exits only after its workers are gone.
	ctx, cancelTimeout := context.WithTimeout(ctx, 15*time.Second)
	defer cancelTimeout()
	c, err := l.Accept(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(12 * time.Second))
	voiceDone := make(chan struct{})
	go func() {
		defer close(voiceDone)
		for {
			b, err := c.ReceiveDatagram(c.Context())
			if err != nil {
				return
			}
			if c.SendDatagram(b) != nil {
				return
			}
		}
	}()
	defer func() { _ = c.Close(); <-voiceDone }()
	if _, err := c.Write(c.ChannelBinding()); err != nil {
		return err
	}
	_, _ = io.Copy(c, c)
	return nil
}

func announce(address string, public []byte) error {
	return json.NewEncoder(os.Stdout).Encode(map[string]string{"address": address, "public_key": hex.EncodeToString(public)})
}

func startProxy(target *net.UDPAddr, loss, blackhole, slow bool) (*net.UDPConn, error) {
	p, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return nil, err
	}
	go func() {
		buffer := make([]byte, 2048)
		var client *net.UDPAddr
		dropped := map[string]bool{}
		acks := 0
		for {
			n, from, err := p.ReadFromUDP(buffer)
			if err != nil {
				return
			}
			to, direction := target, "up"
			if from.String() == target.String() {
				to, direction = client, "down"
			} else {
				client = from
			}
			if to == nil {
				continue
			}
			kind := byte(0)
			if n >= 5 && string(buffer[:4]) == "RN06" {
				kind = buffer[4]
			} else if n > 0 {
				kind = buffer[0] & 0x7f
			}
			id := fmt.Sprintf("%s-%d", direction, kind)
			if slow && direction == "down" && kind == 6 {
				acks++
				if acks == 2 || acks == 3 {
					continue
				}
			}
			if blackhole && direction == "down" && kind == 6 {
				continue
			}
			if loss && (kind == 3 || kind == 6) && !dropped[id] {
				dropped[id] = true
				continue
			}
			_, _ = p.WriteToUDP(buffer[:n], to)
		}
	}()
	return p, nil
}

func printVectors() {
	var key [32]byte
	for i := range key {
		key[i] = 7
	}
	suite := noise.NewCipherSuite(noise.DH25519, noise.CipherChaChaPoly, noise.HashSHA256)
	state := noise.UnsafeNewCipherState(suite, key, 0)
	var frames []string
	for epoch := uint64(0); epoch < 3; epoch++ {
		if epoch != 0 {
			state.Rekey()
		}
		counter := uint32(1)
		if epoch != 0 {
			counter = 2
		}
		header := []byte{4 | byte(epoch&1)*0x80, 0, 0, 0, 0}
		binary.BigEndian.PutUint32(header[1:], counter)
		packet := state.Cipher().Encrypt(header, epoch<<24|uint64(counter), header, []byte("generation"))
		frames = append(frames, hex.EncodeToString(packet))
	}
	_ = json.NewEncoder(os.Stdout).Encode(frames)
}
