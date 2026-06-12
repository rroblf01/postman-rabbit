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
