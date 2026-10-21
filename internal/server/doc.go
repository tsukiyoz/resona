// Package server is the authoritative Resona channel and voice relay service.
// One embedded NATS broker persists bounded chat and CAS-protected KV resources.
// Serve owns resource observation and connection workers; persistence never holds
// the voice routing mutex, and clients receive only their authorized interest scope.
package server
