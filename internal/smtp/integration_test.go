package smtp

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-sasl"
	smtpclient "github.com/emersion/go-smtp"
	"github.com/rroblf01/postman-rabbit/internal/auth"
	"github.com/rroblf01/postman-rabbit/internal/delivery"
	"github.com/rroblf01/postman-rabbit/internal/storage"
)

func startSMTPServer(t *testing.T) (addr string, store *storage.Manager, cleanup func()) {
	t.Helper()
	dir, err := os.MkdirTemp("", "smtp-integration-*")
	if err != nil {
		t.Fatal(err)
	}

	store = storage.New(dir)
	authMgr := auth.New(map[string]string{
		"testuser": "secret",
	})
	del := delivery.New("mail.test.localhost")

	b := NewBackend(authMgr, store, del, nil, "mail.test.localhost", "test.localhost", true)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		os.RemoveAll(dir)
		t.Fatal(err)
	}

	s := smtpclient.NewServer(b)
	s.Domain = "test.localhost"
	s.AllowInsecureAuth = true

	go s.Serve(lis)

	cleanup = func() {
		s.Close()
		lis.Close()
		os.RemoveAll(dir)
	}

	return lis.Addr().String(), store, cleanup
}

func dialSMTP(t *testing.T, addr string) *smtpclient.Client {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	c := smtpclient.NewClient(conn)
	if err := c.Hello("test-client"); err != nil {
		t.Fatalf("HELO: %v", err)
	}
	return c
}

func TestSMTPIntegration_SendLocal(t *testing.T) {
	addr, store, cleanup := startSMTPServer(t)
	defer cleanup()

	c := dialSMTP(t, addr)
	defer c.Close()

	if err := c.Auth(sasl.NewPlainClient("", "testuser", "secret")); err != nil {
		t.Fatalf("AUTH: %v", err)
	}

	if err := c.Mail("sender@other.com", nil); err != nil {
		t.Fatalf("MAIL FROM: %v", err)
	}

	if err := c.Rcpt("testuser@test.localhost", nil); err != nil {
		t.Fatalf("RCPT TO: %v", err)
	}

	wc, err := c.Data()
	if err != nil {
		t.Fatalf("DATA: %v", err)
	}

	msg := "From: sender@other.com\r\nTo: testuser@test.localhost\r\nSubject: Integration Test\r\n\r\nHello from SMTP test!"
	if _, err := strings.NewReader(msg).WriteTo(wc); err != nil {
		t.Fatalf("write DATA: %v", err)
	}
	if err := wc.Close(); err != nil {
		t.Fatalf("close DATA: %v", err)
	}

	us, err := store.ForUser("testuser")
	if err != nil {
		t.Fatalf("ForUser: %v", err)
	}

	newDir := filepath.Join(string(us.Dir()), "new")
	entries, err := os.ReadDir(newDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 message in new/, got %d", len(entries))
	}
}

func TestSMTPIntegration_UnauthenticatedLocalDelivery(t *testing.T) {
	addr, store, cleanup := startSMTPServer(t)
	defer cleanup()

	c := dialSMTP(t, addr)
	defer c.Close()

	if err := c.Mail("anyone@external.com", nil); err != nil {
		t.Fatalf("MAIL FROM: %v", err)
	}

	if err := c.Rcpt("testuser@test.localhost", nil); err != nil {
		t.Fatalf("RCPT TO: %v", err)
	}

	wc, err := c.Data()
	if err != nil {
		t.Fatalf("DATA: %v", err)
	}

	msg := "Subject: Unauthenticated\r\n\r\nBody"
	if _, err := strings.NewReader(msg).WriteTo(wc); err != nil {
		t.Fatalf("write DATA: %v", err)
	}
	if err := wc.Close(); err != nil {
		t.Fatalf("close DATA: %v", err)
	}

	us, err := store.ForUser("testuser")
	if err != nil {
		t.Fatalf("ForUser: %v", err)
	}
	newDir := filepath.Join(string(us.Dir()), "new")
	entries, _ := os.ReadDir(newDir)
	if len(entries) != 1 {
		t.Errorf("expected 1 message, got %d", len(entries))
	}
}

func TestSMTPIntegration_RelayDenied(t *testing.T) {
	addr, _, cleanup := startSMTPServer(t)
	defer cleanup()

	c := dialSMTP(t, addr)
	defer c.Close()

	c.Mail("sender@test.localhost", nil)
	c.Rcpt("someone@external.com", nil)

	wc, err := c.Data()
	if err != nil {
		// Already rejected at DATA command, good
		return
	}
	wc.Write([]byte("Subject: Relay\r\n\r\nBody"))
	_, err = wc.CloseWithResponse()
	if err == nil {
		t.Error("expected error for unauthenticated relay, got none")
	}
}

func TestSMTPIntegration_AuthFailed(t *testing.T) {
	addr, _, cleanup := startSMTPServer(t)
	defer cleanup()

	c := dialSMTP(t, addr)
	defer c.Close()

	err := c.Auth(sasl.NewPlainClient("", "testuser", "wrongpass"))
	if err == nil {
		t.Error("expected auth failure")
	}
}
