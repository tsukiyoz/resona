// Package accounts owns server-local accounts and revocable login credentials.
// SQLite is authoritative for accounts; device keys and server roles are separate.
// Password work and database operations must never run under voice routing locks.
package accounts
