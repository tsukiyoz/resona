package accounts

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
	_ "modernc.org/sqlite"
)

var (
	ErrCredentials = errors.New("invalid account credentials")
	ErrInvalid     = errors.New("invalid account input")
	ErrExists      = errors.New("account name already exists")
	ErrBusy        = errors.New("account service busy")
)

const tokenLifetime = 30 * 24 * time.Hour

// Account contains public account state, never a password hash or login secret.
type Account struct {
	UID      string `json:"uid"`
	Username string `json:"username"`
	Version  uint64 `json:"-"`
}

// Proof identifies a verified, revocable account credential. Recheck it before
// admitting a session when verification and admission are separate operations.
type Proof struct {
	Account
	TokenID string
}

type Store struct {
	db    *sql.DB
	hash  chan struct{}
	dummy string
}

func Open(directory string) (_ *Store, err error) {
	if err = os.MkdirAll(directory, 0o700); err != nil {
		return nil, err
	}
	path, err := filepath.Abs(filepath.Join(directory, "accounts.sqlite"))
	if err != nil {
		return nil, err
	}
	if info, e := os.Lstat(path); e == nil && !info.Mode().IsRegular() {
		return nil, errors.New("account database is not a regular file")
	} else if e != nil && !errors.Is(e, os.ErrNotExist) {
		return nil, e
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err = f.Chmod(0o600); err == nil {
		err = f.Close()
	} else {
		_ = f.Close()
	}
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: path}
	db, err := sql.Open("sqlite", u.String()+"?_pragma=busy_timeout(2000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	s := &Store{db: db, hash: make(chan struct{}, 2)}
	defer func() {
		if err != nil {
			_ = db.Close()
		}
	}()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var version int
	if err = db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return nil, err
	}
	if version > 1 {
		return nil, errors.New("unsupported account database version; original preserved")
	}
	if _, err = db.ExecContext(ctx, "PRAGMA journal_mode=WAL; PRAGMA synchronous=FULL;"); err != nil {
		return nil, err
	}
	if version == 0 {
		_, err = db.ExecContext(ctx, `BEGIN IMMEDIATE;
CREATE TABLE accounts (
 uid TEXT PRIMARY KEY CHECK(length(uid)=64),
 username TEXT UNIQUE,
 password_hash TEXT NOT NULL DEFAULT '',
 legacy_device TEXT UNIQUE,
 version INTEGER NOT NULL DEFAULT 1 CHECK(version>0),
 created INTEGER NOT NULL
);
CREATE TABLE tokens (
 id TEXT PRIMARY KEY, uid TEXT NOT NULL REFERENCES accounts(uid) ON DELETE CASCADE,
 digest TEXT UNIQUE NOT NULL, expires INTEGER NOT NULL, version INTEGER NOT NULL
);
CREATE INDEX tokens_account ON tokens(uid);
CREATE TABLE metadata (key TEXT PRIMARY KEY, value TEXT NOT NULL);
PRAGMA user_version=1;
COMMIT;`)
		if err != nil {
			return nil, err
		}
	}
	s.dummy = passwordHash(rand.Text())
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

// ImportLegacy reserves existing privileged UIDs exactly once. Only the device
// proving the old key can set the first password; public UIDs are not credentials.
func (s *Store) ImportLegacy(ctx context.Context, identities []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var done string
	err = tx.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key='legacy_imported'").Scan(&done)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	for _, uid := range identities {
		if len(uid) != 64 {
			return ErrInvalid
		}
		if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO accounts(uid,legacy_device,created) VALUES(?,?,?)", uid, uid, time.Now().Unix()); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO metadata VALUES('legacy_imported','1')"); err != nil {
		return err
	}
	return tx.Commit()
}

func canonicalName(name string) (string, bool) {
	name = strings.ToLower(name)
	if len(name) < 3 || len(name) > 32 {
		return "", false
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.') {
			return "", false
		}
	}
	return name, true
}

func validPassword(password string) bool {
	return len(password) >= 12 && len(password) <= 256 && utf8.ValidString(password) && !strings.ContainsRune(password, 0)
}

func (s *Store) hashSlot(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case s.hash <- struct{}{}:
		return func() { <-s.hash }, nil
	default:
		return nil, ErrBusy
	}
}

func passwordHash(password string) string {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		panic("system random source unavailable")
	}
	hash := argon2.IDKey([]byte(password), salt, 2, 19*1024, 1, 32)
	enc := base64.RawStdEncoding
	return "$argon2id$v=19$m=19456,t=2,p=1$" + enc.EncodeToString(salt) + "$" + enc.EncodeToString(hash)
}

func passwordMatches(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || strings.Join(parts[:4], "$") != "$argon2id$v=19$m=19456,t=2,p=1" {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) != 16 {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) != 32 {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, 2, 19*1024, 1, 32)
	return subtle.ConstantTimeCompare(got, want) == 1
}

func (s *Store) Register(ctx context.Context, name, password, device string) (Account, error) {
	name, ok := canonicalName(name)
	if !ok || !validPassword(password) {
		return Account{}, ErrInvalid
	}
	release, err := s.hashSlot(ctx)
	if err != nil {
		return Account{}, err
	}
	hash := passwordHash(password)
	release()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Account{}, err
	}
	defer tx.Rollback()
	var existing int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM accounts WHERE username=?", name).Scan(&existing); err != nil {
		return Account{}, err
	}
	if existing != 0 {
		return Account{}, ErrExists
	}
	var uid string
	err = tx.QueryRowContext(ctx, "SELECT uid FROM accounts WHERE legacy_device=? AND username IS NULL", device).Scan(&uid)
	if err == nil {
		_, err = tx.ExecContext(ctx, "UPDATE accounts SET username=?,password_hash=?,legacy_device=NULL WHERE uid=? AND username IS NULL", name, hash, uid)
	} else if errors.Is(err, sql.ErrNoRows) {
		uid = hex.EncodeToString(randomBytes(32))
		_, err = tx.ExecContext(ctx, "INSERT INTO accounts(uid,username,password_hash,created) VALUES(?,?,?,?)", uid, name, hash, time.Now().Unix())
	}
	if err != nil {
		return Account{}, err
	}
	if err = tx.Commit(); err != nil {
		return Account{}, err
	}
	return Account{UID: uid, Username: name, Version: 1}, nil
}

func randomBytes(size int) []byte {
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		panic("system random source unavailable")
	}
	return data
}

func (s *Store) Password(ctx context.Context, name, password string) (Account, error) {
	name, ok := canonicalName(name)
	if !ok || len(password) > 256 {
		return Account{}, ErrCredentials
	}
	release, err := s.hashSlot(ctx)
	if err != nil {
		return Account{}, err
	}
	defer release()
	var a Account
	var hash string
	err = s.db.QueryRowContext(ctx, "SELECT uid,username,password_hash,version FROM accounts WHERE username=?", name).Scan(&a.UID, &a.Username, &hash, &a.Version)
	if errors.Is(err, sql.ErrNoRows) {
		hash = s.dummy
	} else if err != nil {
		return Account{}, err
	}
	valid := passwordMatches(hash, password)
	if !valid || a.UID == "" {
		return Account{}, ErrCredentials
	}
	if err := ctx.Err(); err != nil {
		return Account{}, err
	}
	return a, nil
}

func tokenDigest(secret string) string {
	hash := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(hash[:])
}

func (s *Store) Issue(ctx context.Context, a Account) (Proof, string, error) {
	secret := "ra1_" + rand.Text() + rand.Text()
	id := rand.Text()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Proof{}, "", err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "DELETE FROM tokens WHERE expires<=?", time.Now().Unix()); err != nil {
		return Proof{}, "", err
	}
	var version uint64
	if err = tx.QueryRowContext(ctx, "SELECT version FROM accounts WHERE uid=?", a.UID).Scan(&version); err != nil {
		return Proof{}, "", err
	}
	if version != a.Version {
		return Proof{}, "", ErrCredentials
	}
	var count int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM tokens WHERE uid=?", a.UID).Scan(&count); err != nil {
		return Proof{}, "", err
	}
	if count >= 16 {
		return Proof{}, "", ErrBusy
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO tokens(id,uid,digest,expires,version) VALUES(?,?,?,?,?)", id, a.UID, tokenDigest(secret), time.Now().Add(tokenLifetime).Unix(), a.Version)
	if err != nil {
		return Proof{}, "", err
	}
	if err = tx.Commit(); err != nil {
		return Proof{}, "", err
	}
	return Proof{Account: a, TokenID: id}, secret, nil
}

func (s *Store) Token(ctx context.Context, secret string) (Proof, error) {
	if len(secret) > 256 || !strings.HasPrefix(secret, "ra1_") {
		return Proof{}, ErrCredentials
	}
	var p Proof
	err := s.db.QueryRowContext(ctx, `SELECT a.uid,COALESCE(a.username,''),a.version,t.id
FROM accounts a JOIN tokens t ON t.uid=a.uid
WHERE t.digest=? AND t.expires>? AND t.version=a.version`, tokenDigest(secret), time.Now().Unix()).Scan(&p.UID, &p.Username, &p.Version, &p.TokenID)
	if errors.Is(err, sql.ErrNoRows) {
		return Proof{}, ErrCredentials
	}
	return p, err
}

func (s *Store) Valid(ctx context.Context, p Proof) bool {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM tokens t JOIN accounts a ON a.uid=t.uid
WHERE t.id=? AND t.uid=? AND t.expires>? AND t.version=? AND a.version=t.version`, p.TokenID, p.UID, time.Now().Unix(), p.Version).Scan(&count)
	return err == nil && count == 1
}

func (s *Store) Revoke(ctx context.Context, uid, tokenID string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM tokens WHERE uid=? AND id=?", uid, tokenID)
	return err
}

func (s *Store) ChangePassword(ctx context.Context, a Account, old, password string) (Account, error) {
	if !validPassword(password) {
		return Account{}, ErrInvalid
	}
	current, err := s.Password(ctx, a.Username, old)
	if err != nil || current.UID != a.UID || current.Version != a.Version {
		return Account{}, ErrCredentials
	}
	release, err := s.hashSlot(ctx)
	if err != nil {
		return Account{}, err
	}
	hash := passwordHash(password)
	release()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Account{}, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, "UPDATE accounts SET password_hash=?,version=version+1 WHERE uid=? AND version=?", hash, a.UID, a.Version)
	if err != nil {
		return Account{}, err
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return Account{}, ErrCredentials
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM tokens WHERE uid=?", a.UID); err != nil {
		return Account{}, err
	}
	if err = tx.Commit(); err != nil {
		return Account{}, err
	}
	a.Version++
	return a, nil
}

func (s *Store) Account(ctx context.Context, uid string) (Account, error) {
	var a Account
	err := s.db.QueryRowContext(ctx, "SELECT uid,COALESCE(username,''),version FROM accounts WHERE uid=?", uid).Scan(&a.UID, &a.Username, &a.Version)
	if errors.Is(err, sql.ErrNoRows) {
		err = fmt.Errorf("%w: account unavailable", ErrCredentials)
	}
	return a, err
}
