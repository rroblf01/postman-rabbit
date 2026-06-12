package dkim

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"testing"
)

func generateTestKey(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.CreateTemp("", "dkim-test-*.pem")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := pem.Encode(f, &pem.Block{Type: "PRIVATE KEY", Bytes: der}); err != nil {
		t.Fatal(err)
	}
	return f.Name()
}

func TestNewSignerNoKey(t *testing.T) {
	s, err := NewSigner("sel", "example.com", "")
	if err != nil {
		t.Fatalf("NewSigner with empty key should not error: %v", err)
	}
	if s != nil {
		t.Error("NewSigner should return nil when no key file")
	}
}

func TestNewSignerWithKey(t *testing.T) {
	keyPath := generateTestKey(t)
	defer os.Remove(keyPath)

	s, err := NewSigner("default", "example.com", keyPath)
	if err != nil {
		t.Fatalf("NewSigner = %v", err)
	}
	if s == nil {
		t.Fatal("NewSigner returned nil")
	}
	if s.selector != "default" {
		t.Errorf("selector = %q, want %q", s.selector, "default")
	}
	if s.domain != "example.com" {
		t.Errorf("domain = %q, want %q", s.domain, "example.com")
	}
}

func TestSignerSign(t *testing.T) {
	keyPath := generateTestKey(t)
	defer os.Remove(keyPath)

	s, err := NewSigner("default", "example.com", keyPath)
	if err != nil {
		t.Fatal(err)
	}

	body := []byte("From: alice@example.com\r\nTo: bob@test.com\r\nSubject: Test\r\n\r\nHello World!")
	signed, err := s.Sign(body, "alice@example.com", "<msgid@example.com>")
	if err != nil {
		t.Fatalf("Sign() = %v", err)
	}

	if len(signed) == 0 {
		t.Fatal("Sign returned empty result")
	}

	if len(signed) <= len(body) {
		t.Error("Signed message should be larger than original")
	}

	// Verify the signature header is present
	if !containsBytes(signed, []byte("DKIM-Signature:")) {
		t.Error("Signed message missing DKIM-Signature header")
	}
}

func TestSignerNil(t *testing.T) {
	var s *Signer = nil
	body := []byte("From: test@test.com\r\n\r\nBody")
	result, err := s.Sign(body, "test@test.com", "<id@test.com>")
	if err != nil {
		t.Fatalf("Sign on nil should not error: %v", err)
	}
	if string(result) != string(body) {
		t.Error("Sign on nil should return body unchanged")
	}
}

func containsBytes(haystack, needle []byte) bool {
	return len(haystack) >= len(needle) && bytesContains(haystack, needle) >= 0
}

func bytesContains(b, sub []byte) int {
	for i := 0; i <= len(b)-len(sub); i++ {
		if bytesEqual(b[i:i+len(sub)], sub) {
			return i
		}
	}
	return -1
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
