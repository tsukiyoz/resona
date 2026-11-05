package serverauth

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	"github.com/bytemare/opaque"
)

var ErrInvalidRecord = errors.New("invalid server authentication record")

type credentialRecord struct {
	Version      int    `json:"version"`
	Keys         []byte `json:"keys,omitempty"`
	Registration []byte `json:"registration,omitempty"`
}

// MarshalBinary contains private key material, never the plaintext password.
// The version fixes the OPAQUE suite, role identities and credential encoding.
func (s *Server) MarshalBinary() ([]byte, error) {
	r := credentialRecord{Version: 1}
	if s.Enabled() {
		r.Keys, r.Registration = s.keys.Encode(), s.record.RegistrationRecord.Serialize()
	}
	return json.Marshal(r)
}

func Parse(data []byte) (*Server, error) {
	if len(data) > 4096 {
		return nil, ErrInvalidRecord
	}
	var r credentialRecord
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(&r) != nil || r.Version != 1 {
		return nil, ErrInvalidRecord
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return nil, ErrInvalidRecord
	}
	if len(r.Keys) == 0 && len(r.Registration) == 0 {
		return &Server{}, nil
	}
	c := configuration(nil)
	keys, err := c.DecodeServerKeyMaterial(r.Keys)
	if err != nil || !bytes.Equal(keys.Identity, []byte(serverID)) || len(keys.OPRFGlobalSeed) != 64 {
		return nil, ErrInvalidRecord
	}
	server, err := c.Server()
	if err != nil || server.SetKeyMaterial(keys) != nil {
		return nil, ErrInvalidRecord
	}
	registration, err := server.Deserialize.RegistrationRecord(r.Registration)
	if err != nil {
		return nil, ErrInvalidRecord
	}
	return &Server{keys: keys, record: &opaque.ClientRecord{
		CredentialIdentifier: []byte(clientID), ClientIdentity: []byte(clientID), RegistrationRecord: registration,
	}}, nil
}
