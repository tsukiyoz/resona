package server

import (
	"bytes"
	"context"
	"testing"

	w "github.com/tsukiyoz/resona/internal/nativewire"
)

func TestAccountAdmissionProfileReplacementAndGuestSeparation(t *testing.T) {
	store := testStore(t, t.TempDir())
	s := &Server{config: Config{Store: store, Name: "Test"}, peers: make(map[uint16]*peer), accountGate: make(chan struct{}, 1)}
	newPeer := func(id uint16, nickname string) *peer {
		p := &peer{
			identity: string(bytes.Repeat([]byte{'a' + byte(id)}, 64)), conn: &chatTestConnection{ctx: context.Background()},
			member: w.Member{ID: id, Channel: 1, Nickname: nickname, Epoch: 1}, epoch: 1, out: make(chan []byte, 32), chatWake: make(chan struct{}, 1),
		}
		s.peers[id] = p
		return p
	}
	call := func(p *peer, cmd w.AccountCommand) w.Reply {
		t.Helper()
		s.accountBucket, p.accountBucket = bucket{}, bucket{}
		data, err := w.Pack(w.AccountKind, 1, cmd)
		if err != nil {
			t.Fatal(err)
		}
		f, err := w.Read(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		if !s.accountCommand(p, f) {
			t.Fatal("account handler closed")
		}
		for len(p.out) > 0 {
			f, err = w.Read(bytes.NewReader(<-p.out))
			if err != nil {
				t.Fatal(err)
			}
			if f.Kind == w.ReplyKind {
				var r w.Reply
				if w.Decode(f, &r) != nil {
					t.Fatal("invalid reply")
				}
				return r
			}
		}
		t.Fatal("missing account reply")
		return w.Reply{}
	}
	a := newPeer(1, "Alice")
	if state := s.stateLocked(a); state.ServerRole != "guest" || state.AccountUID != "" || state.CanClaimOwner {
		t.Fatal("device received account privileges")
	}
	registered := call(a, w.AccountCommand{Action: "register", Username: "alice", Password: "correct horse battery staple"})
	if registered.Code != w.OK || registered.AccountToken == "" || a.principal() == a.identity || a.principal() == "" {
		t.Fatal("registration failed")
	}
	uid := a.principal()
	if r := call(a, w.AccountCommand{Action: "update_profile", Nickname: "Alice updated"}); r.Code != w.OK {
		t.Fatal(r.Code)
	}
	b := newPeer(2, "Bob")
	if r := call(b, w.AccountCommand{Action: "password", Username: "alice", Password: "wrong password"}); r.Code != w.AccountInvalid || a.removed {
		t.Fatal("invalid login displaced account")
	}
	logged := call(b, w.AccountCommand{Action: "password", Username: "alice", Password: "correct horse battery staple"})
	if logged.Code != w.OK || b.principal() != uid || b.member.Nickname != "Alice updated" || !a.removed || !a.conn.(*chatTestConnection).closed.Load() {
		t.Fatal("replacement/profile identity incorrect")
	}
	if _, err := store.accounts.Token(context.Background(), registered.AccountToken); err == nil {
		t.Fatal("replaced device can resume")
	}
	// Even an absent old peer must lose its recovery credential after a new login.
	delete(s.peers, 2)
	c := newPeer(3, "Guest")
	if r := call(c, w.AccountCommand{Action: "password", Username: "alice", Password: "correct horse battery staple"}); r.Code != w.OK {
		t.Fatal(r.Code)
	}
	if _, err := store.accounts.Token(context.Background(), logged.AccountToken); err == nil {
		t.Fatal("disconnected old device can steal login during recovery")
	}
	if r := call(c, w.AccountCommand{Action: "logout"}); r.Code != w.OK || c.principal() != "" {
		t.Fatal("logout retained account")
	}
}

func TestAccountPasswordWebLoginRolesAndRevocation(t *testing.T) {
	store := testStore(t, t.TempDir())
	s := &Server{config: Config{Store: store, Name: "Accounts"}, peers: make(map[uint16]*peer)}
	ctx := context.Background()
	a, err := s.config.Store.accounts.Register(ctx, "alice", "correct horse battery staple", "Alice")
	if err != nil {
		t.Fatal(err)
	}
	console, _ := NewAdminConsole(s, testAdminOrigin)
	body := `{"username":"alice","password":"correct horse battery staple"}`
	if r := adminRequest(console, "POST", "/login", body, "", nil); r.Code != 401 {
		t.Fatal("registration granted administration")
	}
	claim, err := store.ProvisionOwnerClaim(false)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Claim(a.UID, claim); err != nil {
		t.Fatal(err)
	}
	r := adminRequest(console, "POST", "/login", body, "", nil)
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	cookie := r.Result().Cookies()[0]
	state := adminRequest(console, "GET", "/state", "", "", cookie)
	if state.Code != 200 || !bytes.Contains(state.Body.Bytes(), []byte("Alice")) || !bytes.Contains(state.Body.Bytes(), []byte(a.UID)) || bytes.Contains(state.Body.Bytes(), []byte("password_hash")) {
		t.Fatal("account projection invalid")
	}
	if _, err = s.config.Store.ResetAccountPassword(ctx, "alice", "another long password"); err != nil {
		t.Fatal(err)
	}
	if r := adminRequest(console, "GET", "/session", "", "", cookie); r.Code != 401 {
		t.Fatal("password reset did not invalidate browser session")
	}
}
