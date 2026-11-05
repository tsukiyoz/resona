// Package serverauth authenticates a shared server password before application
// identity or account credentials are sent over a Noise connection. It binds
// RFC 9807 OPAQUE proofs to the transport's Noise handshake hash.
package serverauth
