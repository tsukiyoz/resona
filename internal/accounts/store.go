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
	"unicode"
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
	Nickname string `json:"nickname"`
	Version  uint64 `json:"-"`
}

// Proof identifies a verified, revocable account credential. Recheck it before
// admitting a session when verification and admission are separate operations.
type Proof struct {
	Account
	TokenID string
	Expires time.Time
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
	uri := sqliteURI(path)
	db, err := sql.Open("sqlite", uri+"?_pragma=busy_timeout(2000)&_pragma=foreign_keys(1)")
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
 username TEXT NOT NULL UNIQUE,
 password_hash TEXT NOT NULL DEFAULT '',
 nickname TEXT NOT NULL DEFAULT '',
 version INTEGER NOT NULL DEFAULT 1 CHECK(version>0),
 created INTEGER NOT NULL
);
CREATE TABLE tokens (
 id TEXT PRIMARY KEY, uid TEXT NOT NULL REFERENCES accounts(uid) ON DELETE CASCADE,
 digest TEXT UNIQUE NOT NULL, expires INTEGER NOT NULL, version INTEGER NOT NULL
);
CREATE INDEX tokens_account ON tokens(uid);
PRAGMA user_version=1;
COMMIT;`)
		if err != nil {
			return nil, err
		}
	}
	s.dummy = passwordHash(rand.Text())
	return s, nil
}

// sqliteURI renders an absolute path as a file URI that SQLite parses as a
// plain path. The two-slash form (file:C:\...) makes SQLite read the drive
// letter as the URI authority and reject the database on Windows, so the path
// is slash-separated with an explicit leading slash:
// file:///C:/Users/name/accounts.sqlite.
func sqliteURI(absolute string) string {
	slashed := filepath.ToSlash(absolute)
	if !strings.HasPrefix(slashed, "/") {
		slashed = "/" + slashed
	}
	return (&url.URL{Scheme: "file", Path: slashed}).String()
}

func (s *Store) Close() error { return s.db.Close() }

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
	return len(password) >= 8 && len(password) <= 256 && utf8.ValidString(password) && !strings.ContainsRune(password, 0)
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

func (s *Store) Register(ctx context.Context, name, password, nickname string) (Account, error) {
	name, ok := canonicalName(name)
	if !ok || !validPassword(password) || !validNickname(nickname) {
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
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM accounts").Scan(&existing); err != nil {
		return Account{}, err
	}
	if existing >= 10000 {
		return Account{}, ErrBusy
	}
	uid := hex.EncodeToString(randomBytes(32))
	_, err = tx.ExecContext(ctx, "INSERT INTO accounts(uid,username,password_hash,nickname,created) VALUES(?,?,?,?,?)", uid, name, hash, nickname, time.Now().Unix())
	if err != nil {
		return Account{}, err
	}
	if err = tx.Commit(); err != nil {
		return Account{}, err
	}
	return Account{UID: uid, Username: name, Nickname: nickname, Version: 1}, nil
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
	err = s.db.QueryRowContext(ctx, "SELECT uid,username,nickname,password_hash,version FROM accounts WHERE username=?", name).Scan(&a.UID, &a.Username, &a.Nickname, &hash, &a.Version)
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

// Issue rotates the account's sole native-session credential. This also revokes
// recovery by an old device whose transport disappeared before replacement.
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
	if _, err = tx.ExecContext(ctx, "DELETE FROM tokens WHERE uid=?", a.UID); err != nil {
		return Proof{}, "", err
	}
	expires := time.Now().Add(tokenLifetime)
	_, err = tx.ExecContext(ctx, "INSERT INTO tokens(id,uid,digest,expires,version) VALUES(?,?,?,?,?)", id, a.UID, tokenDigest(secret), expires.Unix(), a.Version)
	if err != nil {
		return Proof{}, "", err
	}
	if err = tx.Commit(); err != nil {
		return Proof{}, "", err
	}
	return Proof{Account: a, TokenID: id, Expires: expires}, secret, nil
}

func (s *Store) Token(ctx context.Context, secret string) (Proof, error) {
	if len(secret) > 256 || !strings.HasPrefix(secret, "ra1_") {
		return Proof{}, ErrCredentials
	}
	var p Proof
	var expiry int64
	err := s.db.QueryRowContext(ctx, `SELECT a.uid,a.username,a.nickname,a.version,t.id,t.expires
FROM accounts a JOIN tokens t ON t.uid=a.uid
WHERE t.digest=? AND t.expires>? AND t.version=a.version`, tokenDigest(secret), time.Now().Unix()).Scan(&p.UID, &p.Username, &p.Nickname, &p.Version, &p.TokenID, &expiry)
	if errors.Is(err, sql.ErrNoRows) {
		return Proof{}, ErrCredentials
	}
	p.Expires = time.Unix(expiry, 0)
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
	if err != nil {
		return Account{}, err
	}
	if current.UID != a.UID || current.Version != a.Version {
		return Account{}, ErrCredentials
	}
	return s.replacePassword(ctx, a, password)
}

// ResetPassword is for offline server-local recovery under the access lock.
// It preserves the UID and revokes every account token and browser session.
func (s *Store) ResetPassword(ctx context.Context, name, password string) (Account, error) {
	name, ok := canonicalName(name)
	if !ok || !validPassword(password) {
		return Account{}, ErrInvalid
	}
	var uid string
	if err := s.db.QueryRowContext(ctx, "SELECT uid FROM accounts WHERE username=?", name).Scan(&uid); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Account{}, ErrCredentials
		}
		return Account{}, err
	}
	a, err := s.Account(ctx, uid)
	if err != nil {
		return Account{}, err
	}
	return s.replacePassword(ctx, a, password)
}

func (s *Store) replacePassword(ctx context.Context, a Account, password string) (Account, error) {
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
	err := s.db.QueryRowContext(ctx, "SELECT uid,username,nickname,version FROM accounts WHERE uid=?", uid).Scan(&a.UID, &a.Username, &a.Nickname, &a.Version)
	if errors.Is(err, sql.ErrNoRows) {
		err = fmt.Errorf("%w: account unavailable", ErrCredentials)
	}
	return a, err
}

func validNickname(nickname string) bool {
	return strings.TrimSpace(nickname) != "" && utf8.ValidString(nickname) && utf8.RuneCountInString(nickname) <= 30 && strings.IndexFunc(nickname, unicode.IsControl) < 0
}

func (s *Store) UpdateProfile(ctx context.Context, a Account, nickname string) (Account, error) {
	if !validNickname(nickname) {
		return Account{}, ErrInvalid
	}
	result, err := s.db.ExecContext(ctx, "UPDATE accounts SET nickname=? WHERE uid=? AND version=?", nickname, a.UID, a.Version)
	if err != nil {
		return Account{}, err
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return Account{}, ErrCredentials
	}
	a.Nickname = nickname
	return a, nil
}

func (s *Store) List(ctx context.Context) ([]Account, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT uid,username,nickname,version FROM accounts ORDER BY username LIMIT 10000")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Account{}
	for rows.Next() {
		var a Account
		if err = rows.Scan(&a.UID, &a.Username, &a.Nickname, &a.Version); err != nil {
			return nil, err
		}
		result = append(result, a)
	}
	return result, rows.Err()
}
