package client

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestProtocolDestinationCredentials(t *testing.T) {
	s, passwords, profiles := passwordService(t, nil)
	p := profiles.profiles[0]
	legacy := sha256.Sum256([]byte(p.ID + "\x00" + p.Address))
	if passwordKey(p) != hex.EncodeToString(legacy[:]) {
		t.Fatal("legacy TS3 key changed")
	}
	oldKey := passwordKey(p)
	_ = passwords.Set(oldKey, "old-secret")
	p.Protocol = "ts3"
	if _, err := s.SaveServer(p); err != nil {
		t.Fatal(err)
	}
	if found, _ := passwords.Has(oldKey); !found {
		t.Fatal("explicit TS3 lost credentials")
	}
	p.Protocol = "resona"
	if _, err := s.SaveServer(p); err != nil {
		t.Fatal(err)
	}
	if found, _ := passwords.Has(oldKey); found {
		t.Fatal("protocol change retained credentials")
	}
	oldKey = passwordKey(p)
	_ = passwords.Set(oldKey, "native-secret")
	p.CertificateFingerprint = strings.Repeat("ab", 32)
	if _, err := s.SaveServer(p); err != nil {
		t.Fatal(err)
	}
	if found, _ := passwords.Has(oldKey); found {
		t.Fatal("trust change retained credentials")
	}
	if _, err := s.prepareCredentials(p.ID, p.Address, "", false, true); err == nil {
		t.Fatal("accepted credentials prepared for an old destination")
	}
}

func TestNoisePublicKeyDestination(t *testing.T) {
	s, pw, profiles := passwordService(t, nil)
	p := profiles.profiles[0]
	p.Protocol = "resona-noise"
	p.ServerPublicKey = strings.Repeat("ab", 32)
	if _, e := s.SaveServer(p); e != nil {
		t.Fatal(e)
	}
	key := passwordKey(p)
	_ = pw.Set(key, "saved")
	p.ServerPublicKey = strings.ToUpper(p.ServerPublicKey)
	if _, e := s.SaveServer(p); e != nil {
		t.Fatal(e)
	}
	if found, _ := pw.Has(key); !found {
		t.Fatal("case normalization deleted credentials")
	}
	p.ServerPublicKey = strings.Repeat("cd", 32)
	if _, e := s.SaveServer(p); e != nil {
		t.Fatal(e)
	}
	if found, _ := pw.Has(key); found {
		t.Fatal("changed server identity retained credentials")
	}
	p.ServerPublicKey = ""
	if _, e := s.SaveServer(p); e == nil {
		t.Fatal("Noise accepted missing trust key")
	}
}
