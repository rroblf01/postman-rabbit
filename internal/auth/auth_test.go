package auth

import (
	"testing"
)

func TestAuthenticate(t *testing.T) {
	m := New(map[string]string{
		"alice":   "pass123",
		"bob":     "secret",
		"CARLOS":  "UpperCase",
	})

	tests := []struct {
		name     string
		username string
		password string
		want     bool
	}{
		{"valid", "alice", "pass123", true},
		{"wrong pass", "alice", "wrong", false},
		{"unknown user", "eve", "pass", false},
		{"case insensitive user", "Alice", "pass123", true},
		{"uppercase user lowercase", "carlos", "UpperCase", true},
		{"empty password", "alice", "", false},
		{"empty username", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := m.Authenticate(tt.username, tt.password); got != tt.want {
				t.Errorf("Authenticate(%q, %q) = %v, want %v", tt.username, tt.password, got, tt.want)
			}
		})
	}
}

func TestExists(t *testing.T) {
	m := New(map[string]string{
		"alice": "pass",
	})

	if !m.Exists("alice") {
		t.Error("Exists(alice) should be true")
	}
	if m.Exists("bob") {
		t.Error("Exists(bob) should be false")
	}
	if !m.Exists("Alice") {
		t.Error("Exists(Alice) should be true (case insensitive)")
	}
}

func TestUsers(t *testing.T) {
	m := New(map[string]string{
		"alice": "pass1",
		"bob":   "pass2",
	})
	users := m.Users()
	if len(users) != 2 {
		t.Fatalf("Users() returned %d users, want 2", len(users))
	}
	seen := make(map[string]bool)
	for _, u := range users {
		seen[u] = true
	}
	if !seen["alice"] || !seen["bob"] {
		t.Errorf("Users() = %v, missing expected users", users)
	}
}
