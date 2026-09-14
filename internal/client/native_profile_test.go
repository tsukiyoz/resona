package client

import (
	"strings"
	"testing"
)

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
