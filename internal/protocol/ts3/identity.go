package ts3

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/honeybbq/teamspeak-go/crypto"
)

func loadIdentity(ctx context.Context, path string) (*crypto.Identity, error) {
	info, err := os.Lstat(path)
	if err == nil {
		if !info.Mode().IsRegular() || info.Size() > 4096 {
			return nil, errors.New("invalid identity file; original preserved")
		}
		if err := os.Chmod(path, 0600); err != nil {
			return nil, fmt.Errorf("protect identity: %w", err)
		}
		f, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("open identity: %w", err)
		}
		defer f.Close()
		data, err := io.ReadAll(io.LimitReader(f, 4097))
		if err != nil || len(data) > 4096 {
			return nil, errors.New("cannot read identity; original preserved")
		}
		id, err := crypto.IdentityFromString(strings.TrimSpace(string(data)))
		if err != nil {
			return nil, errors.New("invalid identity file; original preserved")
		}
		return id, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect identity: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	id, err := crypto.GenerateIdentity(0)
	if err != nil {
		return nil, errors.New("cannot generate TS3 identity")
	}
	if err := id.UpgradeToLevel(8, ctx); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("create identity directory: %w", err)
	}
	if err := os.Chmod(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("protect identity directory: %w", err)
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".identity-*")
	if err != nil {
		return nil, fmt.Errorf("create identity file: %w", err)
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err = f.WriteString(id.String() + "\n"); err != nil {
		return nil, fmt.Errorf("write identity: %w", err)
	}
	if err = f.Sync(); err != nil {
		return nil, fmt.Errorf("sync identity: %w", err)
	}
	if err = f.Close(); err != nil {
		return nil, err
	}
	// Publish a complete key without replacing an identity from another process.
	if err = os.Link(f.Name(), path); errors.Is(err, os.ErrExist) {
		return loadIdentity(ctx, path)
	} else if err != nil {
		return nil, fmt.Errorf("publish identity: %w", err)
	}
	if err := syncIdentityDirectory(filepath.Dir(path)); err != nil {
		return nil, err
	}
	return id, nil
}

func syncIdentityDirectory(path string) error {
	// Windows cannot flush the read-only directory handle opened by os.Open.
	// The identity file is synced before its non-replacing hard-link publication.
	if runtime.GOOS == "windows" {
		return nil
	}
	dir, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open identity directory: %w", err)
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("sync identity directory: %w", err)
	}
	return nil
}

func identityUID(id *crypto.Identity) string {
	// TS3 defines the UID as base64(SHA1(publicKeyBase64)).
	sum := sha1.Sum([]byte(id.PublicKeyBase64()))
	return base64.StdEncoding.EncodeToString(sum[:])
}
