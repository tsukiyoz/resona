package main

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/tsukiyoz/resona/internal/noiseudp"
)

func newIdentityCommand(configDir string) *cobra.Command {
	var path string
	identity := &cobra.Command{Use: "identity", Short: "Manage the Noise server identity"}
	init := &cobra.Command{
		Use: "init", Short: "Create a private key and print its public key; never overwrite", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			key, err := noiseudp.GenerateKey()
			if err != nil {
				return err
			}
			defer clear(key)
			if err := writeNew(path, key); err != nil {
				return err
			}
			pub, _ := noiseudp.PublicKey(key)
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Noise server public key (X25519): %s\n", hex.EncodeToString(pub))
			return err
		},
	}
	init.Flags().StringVar(&path, "noise-key", filepath.Join(configDir, "noise.key"), "Noise X25519 private key file")
	identity.AddCommand(init)
	return identity
}

func writeNew(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
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
