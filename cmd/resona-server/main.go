package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/big"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	w "github.com/tsukiyoz/resona/internal/nativewire"
	"github.com/tsukiyoz/resona/internal/noiseudp"
	"github.com/tsukiyoz/resona/internal/server"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "resona-server:", err)
		os.Exit(1)
	}
}
func run() error {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	address := flag.String("listen", "127.0.0.1:9988", "QUIC UDP listen address")
	name := flag.String("name", "Resona", "server display name")
	transport := flag.String("transport", "noise", "native transport: noise or quic")
	noiseKeyPath := flag.String("noise-key", filepath.Join(configDir, "resona-server", "noise.key"), "Noise X25519 private key file")
	initKey := flag.Bool("init-key", false, "create a Noise identity and exit; never overwrite")
	certPath := flag.String("cert", filepath.Join(configDir, "resona-server", "cert.pem"), "TLS certificate file")
	keyPath := flag.String("key", filepath.Join(configDir, "resona-server", "key.pem"), "TLS private key file")
	initCert := flag.Bool("init-cert", false, "create a self-signed certificate and exit; never overwrite")
	channelsPath := flag.String("channels", "", "JSON channel array file (ID, Name, Description)")
	maxClients := flag.Int("max-clients", 64, "maximum simultaneous connections (1-64)")
	flag.Parse()
	if flag.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if *transport != "noise" && *transport != "quic" {
		return errors.New("unknown transport")
	}
	if *initKey && *initCert {
		return errors.New("choose one identity initialization mode")
	}
	if *initKey {
		key, e := noiseudp.GenerateKey()
		if e != nil {
			return e
		}
		if e = writeNew(*noiseKeyPath, key); e != nil {
			return e
		}
		pub, _ := noiseudp.PublicKey(key)
		fmt.Printf("Noise server public key (X25519): %s\n", hex.EncodeToString(pub))
		return nil
	}
	if *initCert {
		return createCertificate(*certPath, *keyPath)
	}
	var tlsConfig *tls.Config
	var noiseKey []byte
	if *transport == "noise" {
		noiseKey, err = os.ReadFile(*noiseKeyPath)
		if err != nil {
			return fmt.Errorf("load Noise identity (run --init-key first): %w", err)
		}
		if len(noiseKey) != 32 {
			return errors.New("invalid Noise identity file")
		}
	} else {
		pair, e := tls.LoadX509KeyPair(*certPath, *keyPath)
		if e != nil {
			return fmt.Errorf("load TLS certificate: %w", e)
		}
		tlsConfig = &tls.Config{Certificates: []tls.Certificate{pair}}
	}
	channels := []w.Channel{{ID: 1, Name: "Lobby"}, {ID: 2, Name: "Gaming"}}
	if *channelsPath != "" {
		f, err := os.Open(*channelsPath)
		if err != nil {
			return err
		}
		defer f.Close()
		d := json.NewDecoder(io.LimitReader(f, 65537))
		d.DisallowUnknownFields()
		if err = d.Decode(&channels); err != nil {
			return err
		}
		var extra any
		if err = d.Decode(&extra); err != io.EOF {
			return errors.New("invalid trailing channel configuration")
		}
	}
	s, err := server.Listen(*address, server.Config{NoiseKey: noiseKey, Name: *name, Password: os.Getenv("RESONA_SERVER_PASSWORD"), Channels: channels, MaxClients: *maxClients}, tlsConfig)
	if err != nil {
		return err
	}
	fmt.Printf("Resona native experimental server (%s): %s\n", *transport, s.Addr())
	if *transport == "noise" {
		pub, _ := noiseudp.PublicKey(noiseKey)
		fmt.Printf("Noise server public key (X25519): %s\n", hex.EncodeToString(pub))
	} else {
		fmt.Printf("Certificate SHA-256: %s\n", w.Fingerprint(tlsConfig.Certificates[0].Certificate[0]))
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return s.Serve(ctx)
}
func createCertificate(certPath, keyPath string) error {
	if filepath.Clean(certPath) == filepath.Clean(keyPath) {
		return errors.New("certificate and key paths must differ")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return err
	}
	template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "Resona native server"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().AddDate(1, 0, 0), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, DNSNames: []string{"localhost"}, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	if err = writeNew(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})); err != nil {
		return err
	}
	if err = writeNew(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})); err != nil {
		_ = os.Remove(keyPath)
		return err
	}
	fmt.Printf("Certificate: %s\nPrivate key: %s\nCertificate SHA-256: %s\n", certPath, keyPath, w.Fingerprint(der))
	return nil
}
func writeNew(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(data)
	syncErr := f.Sync()
	closeErr := f.Close()
	err = errors.Join(writeErr, syncErr, closeErr)
	if err != nil {
		_ = os.Remove(path)
	}
	return err
}
