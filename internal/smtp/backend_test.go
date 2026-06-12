package smtp

import (
	"bytes"
	"os"
	"testing"

	"github.com/rroblf01/postman-rabbit/internal/auth"
	"github.com/rroblf01/postman-rabbit/internal/delivery"
	"github.com/rroblf01/postman-rabbit/internal/dkim"
	"github.com/rroblf01/postman-rabbit/internal/storage"
)

func newTestBackend(t *testing.T) (*Backend, *storage.Manager, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "smtp-test-*")
	if err != nil {
		t.Fatal(err)
	}
	store := storage.New(dir)
	authMgr := auth.New(map[string]string{
		"testuser": "testpass",
		"user":     "pass",
	})
	del := delivery.New("test.localhost")
	var dkimSigner *dkim.Signer

	backend := NewBackend(authMgr, store, del, dkimSigner, "test.localhost", "test.com")
	return backend, store, dir
}

func TestNewSession(t *testing.T) {
	backend, _, cleanup := newTestBackend(t)
	defer os.RemoveAll(cleanup)

	sess, err := backend.NewSession(nil)
	if err != nil {
		t.Fatalf("NewSession() = %v", err)
	}
	if sess == nil {
		t.Fatal("NewSession returned nil")
	}
}

func TestSessionAuthMechanisms(t *testing.T) {
	backend, _, cleanup := newTestBackend(t)
	defer os.RemoveAll(cleanup)

	sess, _ := backend.NewSession(nil)
	s := sess.(*Session)

	mechs := s.AuthMechanisms()
	if len(mechs) != 2 {
		t.Fatalf("AuthMechanisms returned %d, want 2", len(mechs))
	}
	if mechs[0] != "PLAIN" || mechs[1] != "LOGIN" {
		t.Errorf("AuthMechanisms = %v, want [PLAIN LOGIN]", mechs)
	}
}

func TestSessionMailRcpt(t *testing.T) {
	backend, _, cleanup := newTestBackend(t)
	defer os.RemoveAll(cleanup)

	sess, _ := backend.NewSession(nil)
	s := sess.(*Session)

	if err := s.Mail("sender@test.com", nil); err != nil {
		t.Fatalf("Mail() = %v", err)
	}
	if s.from != "sender@test.com" {
		t.Errorf("from = %q, want %q", s.from, "sender@test.com")
	}

	if err := s.Rcpt("alice@test.com", nil); err != nil {
		t.Fatalf("Rcpt() = %v", err)
	}
	if len(s.recipients) != 1 || s.recipients[0] != "alice@test.com" {
		t.Errorf("recipients = %v", s.recipients)
	}
}

func TestSessionReset(t *testing.T) {
	backend, _, cleanup := newTestBackend(t)
	defer os.RemoveAll(cleanup)

	sess, _ := backend.NewSession(nil)
	s := sess.(*Session)

	s.Mail("test@test.com", nil)
	s.Rcpt("rcpt@test.com", nil)

	s.Reset()
	if s.from != "" || len(s.recipients) != 0 {
		t.Error("Reset should clear from and recipients")
	}
}

func TestSessionLogout(t *testing.T) {
	backend, _, cleanup := newTestBackend(t)
	defer os.RemoveAll(cleanup)

	sess, _ := backend.NewSession(nil)
	if err := sess.Logout(); err != nil {
		t.Errorf("Logout() = %v", err)
	}
}

func TestSessionDeliverLocal(t *testing.T) {
	backend, store, cleanup := newTestBackend(t)
	defer os.RemoveAll(cleanup)

	// Ensure testuser exists in storage too
	store.ForUser("testuser")

	sess, _ := backend.NewSession(nil)
	s := sess.(*Session)
	s.username = "testuser"
	s.authenticated = true

	s.Mail("test@test.com", nil)
	s.Rcpt("testuser@test.com", nil)

	msg := []byte("From: test@test.com\r\nTo: testuser@test.com\r\nSubject: Test\r\n\r\nHello!")
	if err := s.Data(bytes.NewReader(msg)); err != nil {
		t.Fatalf("Data() = %v", err)
	}
}

func TestSessionRelayUnauthenticated(t *testing.T) {
	backend, _, cleanup := newTestBackend(t)
	defer os.RemoveAll(cleanup)

	sess, _ := backend.NewSession(nil)
	s := sess.(*Session)
	s.authenticated = false

	s.Mail("test@test.com", nil)
	s.Rcpt("external@other.com", nil)

	msg := []byte("From: test@test.com\r\nSubject: Test\r\n\r\nBody")
	if err := s.Data(bytes.NewReader(msg)); err == nil {
		t.Error("Expected error for unauthenticated relay")
	}
}

func TestAuthPLAIN_Valid(t *testing.T) {
	backend, _, cleanup := newTestBackend(t)
	defer os.RemoveAll(cleanup)

	sess, _ := backend.NewSession(nil)
	s := sess.(*Session)

	srv, err := s.Auth("PLAIN")
	if err != nil {
		t.Fatalf("Auth(PLAIN) = %v", err)
	}

	ch, done, err := srv.Next(nil)
	if err != nil {
		t.Fatalf("Next(nil) = %v", err)
	}
	if done {
		t.Error("Next(nil) returned done=true, want false")
	}
	if ch == nil {
		t.Error("Next(nil) returned nil challenge")
	}

	authResp := []byte("\x00testuser\x00testpass")
	ch, done, err = srv.Next(authResp)
	if err != nil {
		t.Fatalf("Next(auth) = %v", err)
	}
	if !done {
		t.Error("Next(auth) returned done=false, want true")
	}
	if !s.authenticated {
		t.Error("session should be authenticated")
	}
	if s.username != "testuser" {
		t.Errorf("username = %q, want %q", s.username, "testuser")
	}
	_ = ch
}

func TestAuthPLAIN_Invalid(t *testing.T) {
	backend, _, cleanup := newTestBackend(t)
	defer os.RemoveAll(cleanup)

	sess, _ := backend.NewSession(nil)
	s := sess.(*Session)

	srv, err := s.Auth("PLAIN")
	if err != nil {
		t.Fatal(err)
	}

	_, _, _ = srv.Next(nil)
	_, _, err = srv.Next([]byte("\x00testuser\x00wrongpass"))
	if err == nil {
		t.Error("expected error for wrong password")
	}
	if s.authenticated {
		t.Error("session should not be authenticated")
	}
}

func TestAuthPLAIN_UnexpectedCall(t *testing.T) {
	backend, _, cleanup := newTestBackend(t)
	defer os.RemoveAll(cleanup)

	sess, _ := backend.NewSession(nil)
	s := sess.(*Session)

	srv, _ := s.Auth("PLAIN")
	srv.Next(nil)                                     // first call
	srv.Next([]byte("\x00testuser\x00testpass"))       // second call, auth succeeds
	_, _, err := srv.Next([]byte("extra"))             // third call should fail
	if err == nil {
		t.Error("expected error for unexpected call after auth done")
	}
}

func TestAuthLOGIN_Valid(t *testing.T) {
	backend, _, cleanup := newTestBackend(t)
	defer os.RemoveAll(cleanup)

	sess, _ := backend.NewSession(nil)
	s := sess.(*Session)

	srv, err := s.Auth("LOGIN")
	if err != nil {
		t.Fatalf("Auth(LOGIN) = %v", err)
	}

	ls := srv.(*loginServer)
	if ls.step != 0 {
		t.Errorf("step = %d, want 0", ls.step)
	}

	// Step 0: server sends Username challenge
	ch, done, err := srv.Next(nil)
	if err != nil {
		t.Fatalf("step 0: %v", err)
	}
	if string(ch) != "Username:" {
		t.Errorf("step 0 challenge = %q, want %q", string(ch), "Username:")
	}
	if done {
		t.Error("step 0: done should be false")
	}

	// Step 1: send username, server sends Password challenge
	ch, done, err = srv.Next([]byte("testuser"))
	if err != nil {
		t.Fatalf("step 1: %v", err)
	}
	if string(ch) != "Password:" {
		t.Errorf("step 1 challenge = %q, want %q", string(ch), "Password:")
	}
	if done {
		t.Error("step 1: done should be false")
	}

	// Step 2: send password, auth succeeds
	ch, done, err = srv.Next([]byte("testpass"))
	if err != nil {
		t.Fatalf("step 2: %v", err)
	}
	if !done {
		t.Error("step 2: done should be true")
	}
	if !s.authenticated {
		t.Error("session should be authenticated after LOGIN")
	}
	if s.username != "testuser" {
		t.Errorf("username = %q, want %q", s.username, "testuser")
	}
	_ = ch
}

func TestAuthLOGIN_InvalidPassword(t *testing.T) {
	backend, _, cleanup := newTestBackend(t)
	defer os.RemoveAll(cleanup)

	sess, _ := backend.NewSession(nil)
	s := sess.(*Session)

	srv, _ := s.Auth("LOGIN")

	srv.Next(nil)            // step 0
	srv.Next([]byte("testuser"))  // step 1
	_, _, err := srv.Next([]byte("wrongpass")) // step 2
	if err == nil {
		t.Error("expected error for wrong password")
	}
	if s.authenticated {
		t.Error("session should not be authenticated after failed LOGIN")
	}
}

func TestAuth_UnknownMechanism(t *testing.T) {
	backend, _, cleanup := newTestBackend(t)
	defer os.RemoveAll(cleanup)

	sess, _ := backend.NewSession(nil)
	s := sess.(*Session)

	_, err := s.Auth("CRAM-MD5")
	if err == nil {
		t.Error("expected error for unknown mechanism")
	}
}

func TestAuth_RepeatedLoginResetsState(t *testing.T) {
	backend, _, cleanup := newTestBackend(t)
	defer os.RemoveAll(cleanup)

	sess, _ := backend.NewSession(nil)
	s := sess.(*Session)

	srv, _ := s.Auth("PLAIN")
	srv.Next(nil)
	srv.Next([]byte("\x00testuser\x00testpass"))

	if !s.authenticated {
		t.Fatal("first auth should succeed")
	}

	srv2, _ := s.Auth("PLAIN")
	srv2.Next(nil)
	srv2.Next([]byte("\x00user\x00pass"))
	if !s.authenticated {
		t.Error("second auth should also succeed")
	}
	if s.username != "user" {
		t.Errorf("username should be updated: got %q, want %q", s.username, "user")
	}
}

func TestSessionPartitionRecipients(t *testing.T) {
	backend, _, cleanup := newTestBackend(t)
	defer os.RemoveAll(cleanup)

	sess, _ := backend.NewSession(nil)
	s := sess.(*Session)
	s.username = "testuser"

	s.Rcpt("user@test.com", nil)   // local (backend.domain = test.com)
	s.Rcpt("other@gmail.com", nil) // remote

	local, remote := s.partitionRecipients()
	if len(local) != 1 || local[0] != "user" {
		t.Errorf("local = %v, want [user]", local)
	}
	if len(remote) != 1 || remote[0] != "other@gmail.com" {
		t.Errorf("remote = %v, want [other@gmail.com]", remote)
	}
}
