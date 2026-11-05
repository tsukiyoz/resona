package serverauth

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestCredentialRecordValidation(t *testing.T) {
	s, err := New("private-test-password")
	if err != nil {
		t.Fatal(err)
	}
	data, err := s.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("private-test-password")) {
		t.Fatal("plaintext password persisted")
	}
	var original credentialRecord
	if err := json.Unmarshal(data, &original); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		edit func(*credentialRecord)
	}{
		{"version", func(r *credentialRecord) { r.Version++ }},
		{"missing keys", func(r *credentialRecord) { r.Keys = nil }},
		{"missing record", func(r *credentialRecord) { r.Registration = nil }},
		{"bad keys", func(r *credentialRecord) { r.Keys = []byte("invalid") }},
		{"bad record", func(r *credentialRecord) { r.Registration = []byte("invalid") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := original
			tc.edit(&r)
			b, _ := json.Marshal(r)
			if _, err := Parse(b); err == nil {
				t.Fatal("invalid credential accepted")
			}
		})
	}
	for _, b := range [][]byte{nil, []byte(`{"version":1,"extra":1}`), append(bytes.Clone(data), []byte(` {}`)...), bytes.Repeat([]byte("x"), 4097)} {
		if _, err := Parse(b); err == nil {
			t.Fatal("invalid encoding accepted")
		}
	}
}
