package config

import (
	"os"
	"testing"
)

func setEnv(t *testing.T, key, val string) {
	t.Helper()
	if err := os.Setenv(key, val); err != nil {
		t.Fatal(err)
	}
}

func unsetEnv(t *testing.T, key string) {
	t.Helper()
	if err := os.Unsetenv(key); err != nil {
		t.Fatal(err)
	}
}

func cleanupEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"HOSTNAME", "DOMAIN", "MAILDIR", "TLS_CERT", "TLS_KEY",
		"SMTP_PORT", "SUBMISSION_PORT", "SMTPS_PORT", "IMAP_PORT", "IMAPS_PORT",
		"DKIM_SELECTOR", "DKIM_KEY_FILE", "RELAY_ENABLED", "USERS"} {
		os.Unsetenv(k)
	}
}

func TestFromEnvDefaults(t *testing.T) {
	cleanupEnv(t)

	// Must fail without USERS
	if _, err := FromEnv(); err == nil {
		t.Fatal("expected error without USERS")
	}

	setEnv(t, "USERS", "test:pass")
	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv() = %v", err)
	}

	if cfg.Hostname != "mail.localhost" {
		t.Errorf("Hostname = %q, want %q", cfg.Hostname, "mail.localhost")
	}
	if cfg.Domain != "localhost" {
		t.Errorf("Domain = %q, want %q", cfg.Domain, "localhost")
	}
	if cfg.SMTPPort != 25 {
		t.Errorf("SMTPPort = %d, want %d", cfg.SMTPPort, 25)
	}
	if cfg.IMAPPort != 143 {
		t.Errorf("IMAPPort = %d, want %d", cfg.IMAPPort, 143)
	}
	if len(cfg.Users) != 1 {
		t.Errorf("len(Users) = %d, want 1", len(cfg.Users))
	}
	if cfg.Users["test"] != "pass" {
		t.Errorf("Users[test] = %q, want %q", cfg.Users["test"], "pass")
	}
	if !cfg.RelayEnabled {
		t.Error("RelayEnabled should be true")
	}
}

func TestFromEnvCustom(t *testing.T) {
	cleanupEnv(t)
	setEnv(t, "HOSTNAME", "mx.example.com")
	setEnv(t, "DOMAIN", "example.com")
	setEnv(t, "MAILDIR", "/custom/mail")
	setEnv(t, "SMTP_PORT", "2525")
	setEnv(t, "SUBMISSION_PORT", "2587")
	setEnv(t, "IMAP_PORT", "1143")
	setEnv(t, "RELAY_ENABLED", "false")
	setEnv(t, "USERS", "alice:abc,bob:xyz")

	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv() = %v", err)
	}

	if cfg.Hostname != "mx.example.com" {
		t.Errorf("Hostname = %q", cfg.Hostname)
	}
	if cfg.Domain != "example.com" {
		t.Errorf("Domain = %q", cfg.Domain)
	}
	if cfg.SMTPPort != 2525 {
		t.Errorf("SMTPPort = %d", cfg.SMTPPort)
	}
	if cfg.SubmissionPort != 2587 {
		t.Errorf("SubmissionPort = %d", cfg.SubmissionPort)
	}
	if cfg.IMAPPort != 1143 {
		t.Errorf("IMAPPort = %d", cfg.IMAPPort)
	}
	if cfg.RelayEnabled {
		t.Error("RelayEnabled should be false")
	}
	if cfg.Users["alice"] != "abc" || cfg.Users["bob"] != "xyz" {
		t.Errorf("Users = %v", cfg.Users)
	}
}

func TestFromEnvTLSMismatch(t *testing.T) {
	cleanupEnv(t)
	setEnv(t, "USERS", "test:pass")
	setEnv(t, "TLS_CERT", "/path/cert.pem")

	if _, err := FromEnv(); err == nil {
		t.Fatal("expected error with TLS_CERT but no TLS_KEY")
	}
}

func TestFromEnvInvalidUser(t *testing.T) {
	cleanupEnv(t)
	setEnv(t, "USERS", "nopass")

	if _, err := FromEnv(); err == nil {
		t.Fatal("expected error with invalid user entry")
	}
}

func TestListenAddrs(t *testing.T) {
	cleanupEnv(t)
	setEnv(t, "USERS", "test:pass")
	setEnv(t, "SMTPS_PORT", "0")
	setEnv(t, "IMAPS_PORT", "0")

	cfg, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}

	addrs := cfg.ListenAddrs()
	expected := []string{
		"0.0.0.0:25",
		"0.0.0.0:587",
		"0.0.0.0:143",
	}

	if len(addrs) != len(expected) {
		t.Fatalf("ListenAddrs() = %v, want %v", addrs, expected)
	}

	for i, a := range addrs {
		if a != expected[i] {
			t.Errorf("ListenAddrs[%d] = %q, want %q", i, a, expected[i])
		}
	}
}
