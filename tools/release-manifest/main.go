// release-manifest signs byte-exact update metadata. Signing keys are never printed.
package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/tsukiyoz/resona/internal/updates"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	version := flag.String("version", "", "immutable release version, e.g. v0.2.0")
	assetsPath := flag.String("assets", "", "JSON array of {platform,path,url}")
	output := flag.String("out", "", "signed manifest output path")
	expires := flag.String("expires", "", "expiry in RFC3339 UTC")
	notesPath := flag.String("notes", "", "optional UTF-8 release note file")
	flag.Parse()
	private, err := base64.StdEncoding.DecodeString(os.Getenv("RESONA_UPDATE_SIGNING_KEY"))
	if err != nil || len(private) != ed25519.PrivateKeySize {
		return errors.New("RESONA_UPDATE_SIGNING_KEY must contain a base64 Ed25519 private key")
	}
	if *assetsPath == "" || *output == "" {
		return errors.New("-assets and -out are required")
	}
	expiry, err := time.Parse(time.RFC3339, *expires)
	if err != nil {
		return errors.New("-expires must be RFC3339 UTC")
	}
	var inputs []struct {
		Platform string `json:"platform"`
		Path     string `json:"path"`
		URL      string `json:"url"`
	}
	data, err := os.ReadFile(*assetsPath)
	if err != nil {
		return err
	}
	if len(data) > 64<<10 || json.Unmarshal(data, &inputs) != nil || len(inputs) == 0 {
		return errors.New("invalid asset input")
	}
	manifest := updates.Manifest{Schema: 1, Product: "resona", Channel: "stable", Version: *version, Expires: expiry, Assets: make(map[string]updates.Asset)}
	if *notesPath != "" {
		notes, err := os.ReadFile(*notesPath)
		if err != nil {
			return err
		}
		manifest.Notes = string(notes)
	}
	for _, input := range inputs {
		if _, exists := manifest.Assets[input.Platform]; exists {
			return errors.New("duplicate platform")
		}
		if input.Platform != "windows/amd64" && input.Platform != "darwin/arm64" && input.Platform != "darwin/amd64" {
			return errors.New("unsupported platform")
		}
		if input.Platform == "windows/amd64" {
			if err := updates.ValidateWindowsArchive(input.Path, *version); err != nil {
				return err
			}
		}
		f, err := os.Open(input.Path)
		if err != nil {
			return err
		}
		h := sha256.New()
		size, err := io.Copy(h, io.LimitReader(f, updates.MaxPackageSize+1))
		f.Close()
		if err != nil {
			return err
		}
		manifest.Assets[input.Platform] = updates.Asset{URL: input.URL, SHA256: hex.EncodeToString(h.Sum(nil)), Size: size}
	}
	payload, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	key := ed25519.PrivateKey(private)
	signed, err := json.Marshal(updates.SignedManifest{Payload: payload, Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(key, payload))})
	if err != nil {
		return err
	}
	public := base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey))
	if _, err := updates.Verify(signed, public, time.Now()); err != nil {
		return err
	}
	if err := os.WriteFile(*output, signed, 0o644); err != nil {
		return err
	}
	fmt.Println("Signed manifest written; verification public key:", public)
	return nil
}
