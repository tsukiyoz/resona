// Package noiseudp implements Resona authenticated Noise UDP sessions.
// It owns handshake, replay protection, reliable control and datagram transport,
// including background rekeying. Callers must close sessions to stop workers.
package noiseudp
