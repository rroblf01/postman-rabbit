package auth

import (
	"testing"
)

func TestAuthenticate(t *testing.T) {
	m := New(map[string]string{
		"alice":  "pass123",
		"bob":    "secret",
		"CARLOS": "UpperCase",
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

func TestAuthenticateLongCredentials(t *testing.T) {
	longPass := string(make([]byte, 10000))
	m := New(map[string]string{
		"normal": "pass",
		"user":   longPass,
	})

	if m.Authenticate("normal", longPass) {
		t.Error("long password for normal user should fail")
	}
	if !m.Authenticate("user", longPass) {
		t.Error("user with long password should authenticate")
	}
	if m.Authenticate("unknown", longPass) {
		t.Error("unknown user should not authenticate")
	}
}

func TestAuthenticateSpecialChars(t *testing.T) {
	m := New(map[string]string{
		"user@domain": "p@ss!",
		"user name":   "pass word",
		"user\n":      "pass",
	})

	if !m.Authenticate("user@domain", "p@ss!") {
		t.Error("special chars in username/password should work")
	}
	if !m.Authenticate("user name", "pass word") {
		t.Error("spaces in credentials should work")
	}
	if !m.Authenticate("user\n", "pass") {
		t.Error("newline in username should work")
	}
}

func TestAuthenticateConcurrent(t *testing.T) {
	m := New(map[string]string{
		"alice": "secret",
	})

	n := 20
	errc := make(chan bool, n)
	for i := 0; i < n; i++ {
		go func() {
			errc <- m.Authenticate("alice", "secret")
		}()
	}
	for i := 0; i < n; i++ {
		if !<-errc {
			t.Error("concurrent auth failed")
		}
	}
}

func TestAuthenticateEmptyMap(t *testing.T) {
	m := New(nil)
	if m.Authenticate("anyone", "anything") {
		t.Error("empty user map should not authenticate anyone")
	}
	if m.Exists("anyone") {
		t.Error("empty user map should not find anyone")
	}
}

func TestExistsEmptyUsername(t *testing.T) {
	m := New(map[string]string{"": "pass"})
	if !m.Exists("") {
		t.Error("empty username user should exist")
	}
}
