// Package updates implements signed, whole-package desktop updates outside audio paths.
package updates

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Set by release builds. Unconfigured builds never use an unsigned update feed.
var ManifestURL, PublicKey string

const MaxPackageSize int64 = 512 << 20

type Asset struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type Manifest struct {
	Schema  int              `json:"schema"`
	Product string           `json:"product"`
	Channel string           `json:"channel"`
	Version string           `json:"version"`
	Expires time.Time        `json:"expires"`
	Notes   string           `json:"notes"`
	Assets  map[string]Asset `json:"assets"`
}

type SignedManifest struct {
	Payload   json.RawMessage `json:"payload"`
	Signature string          `json:"signature"`
}

func strictJSON(data []byte, dest any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(dest); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("unexpected trailing data")
	}
	return nil
}

func Verify(data []byte, publicKey string, now time.Time) (Manifest, error) {
	var signed SignedManifest
	var manifest Manifest
	key, err := base64.StdEncoding.DecodeString(publicKey)
	if err != nil || len(key) != ed25519.PublicKeySize {
		return manifest, errors.New("更新签名公钥未配置或无效")
	}
	if len(data) > 64<<10 || strictJSON(data, &signed) != nil {
		return manifest, errors.New("更新清单格式无效")
	}
	sig, err := base64.StdEncoding.DecodeString(signed.Signature)
	if err != nil || !ed25519.Verify(key, signed.Payload, sig) {
		return manifest, errors.New("更新清单签名验证失败")
	}
	if strictJSON(signed.Payload, &manifest) != nil || manifest.Schema != 1 || manifest.Product != "resona" || manifest.Channel != "stable" {
		return manifest, errors.New("不支持此更新清单版本或产品")
	}
	if _, err := versionParts(manifest.Version); err != nil {
		return manifest, err
	}
	if !manifest.Expires.After(now) || len(manifest.Notes) > 8192 || len(manifest.Assets) > 8 {
		return manifest, errors.New("更新清单已过期或超出限制")
	}
	for _, asset := range manifest.Assets {
		digest, err := hex.DecodeString(asset.SHA256)
		if err != nil || len(digest) != 32 || asset.Size <= 0 || asset.Size > MaxPackageSize || !httpsURL(asset.URL) {
			return manifest, errors.New("更新包信息无效")
		}
	}
	return manifest, nil
}

func httpsURL(value string) bool {
	u, err := url.Parse(value)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && u.Fragment == ""
}

func versionParts(value string) ([3]uint64, error) {
	var result [3]uint64
	parts := strings.Split(strings.TrimPrefix(value, "v"), ".")
	if len(parts) != 3 {
		return result, errors.New("更新仅支持正式的三段版本号")
	}
	for i, part := range parts {
		if part == "" || (len(part) > 1 && part[0] == '0') {
			return result, errors.New("更新版本号无效")
		}
		for _, c := range part {
			if c < '0' || c > '9' {
				return result, errors.New("更新版本号无效")
			}
		}
		n, err := strconv.ParseUint(part, 10, 32)
		if err != nil {
			return result, errors.New("更新版本号无效")
		}
		result[i] = n
	}
	return result, nil
}

func Newer(candidate, current string) (bool, error) {
	newVersion, err := versionParts(candidate)
	if err != nil {
		return false, err
	}
	oldVersion, err := versionParts(current)
	if err != nil {
		return false, errors.New("开发版本不自动更新，请使用正式发布包")
	}
	for i := range newVersion {
		if newVersion[i] != oldVersion[i] {
			return newVersion[i] > oldVersion[i], nil
		}
	}
	return false, nil
}
