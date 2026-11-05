package serverauth

import (
	"errors"
	"io"

	"github.com/bytemare/opaque"
)

const (
	clientID = "resona-guest"
	serverID = "resona-server"
	ke1Size  = 96
	ke2Size  = 320
	ke3Size  = 64
)

var ErrAuthentication = errors.New("server password authentication failed")

// Server holds one immutable OPAQUE registration for the shared connection
// password. This is separate from user account registration and token storage.
type Server struct {
	keys   *opaque.ServerKeyMaterial
	record *opaque.ClientRecord
}

func configuration(binding []byte) *opaque.Configuration {
	c := opaque.DefaultConfiguration()
	// Identity KSF avoids memory-hard work at every voice reconnect; this suite
	// requires a strong shared password and does not harden stolen credentials.
	// User account password storage continues to use Argon2 independently.
	c.KSF = 0
	c.Context = append([]byte("resona-server-password-v1\x00"), binding...)
	return c
}

func (s *Server) Enabled() bool { return s.record != nil }

func New(password string) (*Server, error) {
	if password == "" {
		return &Server{}, nil
	}
	c := configuration(nil)
	private, public := c.KeyGen()
	s := &Server{keys: &opaque.ServerKeyMaterial{
		Identity: []byte(serverID), PrivateKey: private,
		PublicKeyBytes: public.Encode(), OPRFGlobalSeed: c.GenerateOPRFSeed(),
	}}
	server, err := c.Server()
	if err != nil {
		return nil, err
	}
	if err = server.SetKeyMaterial(s.keys); err != nil {
		return nil, err
	}
	client, err := c.Client()
	if err != nil {
		return nil, err
	}
	defer client.ClearState()
	request, err := client.RegistrationInit([]byte(password))
	if err != nil {
		return nil, err
	}
	response, err := server.RegistrationResponse(request, []byte(clientID), nil)
	if err != nil {
		return nil, err
	}
	record, export, err := client.RegistrationFinalize(response, []byte(clientID), []byte(serverID))
	clear(export)
	if err != nil {
		return nil, err
	}
	s.record = &opaque.ClientRecord{CredentialIdentifier: []byte(clientID), ClientIdentity: []byte(clientID), RegistrationRecord: record}
	return s, nil
}

// Accept must run with a bounded stream deadline and connection admission limit.
// Noise's handshake hash binds PAKE to this exact encrypted transport, preventing
// a proxy from relaying authentication across two different Noise connections.
func (s *Server) Accept(rw io.ReadWriter, binding []byte) error {
	if len(binding) != 32 {
		return ErrAuthentication
	}
	if s.record == nil {
		return write(rw, []byte{0})
	}
	if err := write(rw, []byte{1}); err != nil {
		return err
	}
	server, err := configuration(binding).Server()
	if err != nil {
		return err
	}
	if err = server.SetKeyMaterial(s.keys); err != nil {
		return err
	}
	input := make([]byte, ke1Size)
	if _, err = io.ReadFull(rw, input); err != nil {
		return err
	}
	ke1, err := server.Deserialize.KE1(input)
	if err != nil {
		return ErrAuthentication
	}
	ke2, output, err := server.GenerateKE2(ke1, s.record)
	if err != nil {
		return ErrAuthentication
	}
	defer clear(output.SessionSecret)
	defer clear(output.ClientMAC)
	if err = write(rw, ke2.Serialize()); err != nil {
		return err
	}
	input = make([]byte, ke3Size)
	if _, err = io.ReadFull(rw, input); err != nil {
		return err
	}
	ke3, err := server.Deserialize.KE3(input)
	if err != nil || server.LoginFinish(ke3, output.ClientMAC) != nil {
		return ErrAuthentication
	}
	return write(rw, []byte{1})
}

// Authenticate is the Go test/tool client counterpart. A public server is only
// accepted with an independently supplied Noise key and no expected password.
func Authenticate(rw io.ReadWriter, binding []byte, password string, pinned bool) error {
	if len(binding) != 32 || (!pinned && password == "") {
		return ErrAuthentication
	}
	mode := []byte{0}
	if _, err := io.ReadFull(rw, mode); err != nil {
		return err
	}
	if mode[0] == 0 && pinned && password == "" {
		return nil
	}
	if mode[0] != 1 || password == "" {
		return ErrAuthentication
	}
	client, err := configuration(binding).Client()
	if err != nil {
		return err
	}
	defer client.ClearState()
	ke1, err := client.GenerateKE1([]byte(password))
	if err != nil {
		return err
	}
	if err = write(rw, ke1.Serialize()); err != nil {
		return err
	}
	input := make([]byte, ke2Size)
	if _, err = io.ReadFull(rw, input); err != nil {
		return err
	}
	ke2, err := client.Deserialize.KE2(input)
	if err != nil {
		return ErrAuthentication
	}
	ke3, secret, export, err := client.GenerateKE3(ke2, []byte(clientID), []byte(serverID))
	clear(secret)
	clear(export)
	if err != nil {
		return ErrAuthentication
	}
	if err = write(rw, ke3.Serialize()); err != nil {
		return err
	}
	if _, err = io.ReadFull(rw, mode); err != nil {
		return err
	}
	if mode[0] != 1 {
		return ErrAuthentication
	}
	return nil
}

func write(w io.Writer, b []byte) error {
	n, err := w.Write(b)
	if err == nil && n != len(b) {
		err = io.ErrShortWrite
	}
	return err
}
