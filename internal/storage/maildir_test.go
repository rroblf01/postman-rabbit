package storage

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewManager(t *testing.T) {
	dir, err := os.MkdirTemp("", "maildir-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	m := New(dir)
	if m.BaseDir() != dir {
		t.Errorf("BaseDir = %q, want %q", m.BaseDir(), dir)
	}
}

func TestForUser(t *testing.T) {
	dir, err := os.MkdirTemp("", "maildir-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	m := New(dir)
	us, err := m.ForUser("alice")
	if err != nil {
		t.Fatalf("ForUser(alice) = %v", err)
	}
	if us == nil {
		t.Fatal("ForUser returned nil")
	}

	userDir := filepath.Join(dir, "alice")
	if _, err := os.Stat(userDir); os.IsNotExist(err) {
		t.Error("user directory was not created")
	}

	// subdirs new, cur, tmp should exist
	for _, sub := range []string{"new", "cur", "tmp"} {
		subPath := filepath.Join(userDir, sub)
		if _, err := os.Stat(subPath); os.IsNotExist(err) {
			t.Errorf("maildir subdirectory %s not created", sub)
		}
	}

	// Calling ForUser again should return the same storage
	us2, err := m.ForUser("alice")
	if err != nil {
		t.Fatal(err)
	}
	if us2 != us {
		t.Error("ForUser returned different storage for same user")
	}
}

func TestDeliver(t *testing.T) {
	dir, err := os.MkdirTemp("", "maildir-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	m := New(dir)
	us, err := m.ForUser("bob")
	if err != nil {
		t.Fatal(err)
	}

	msgContent := "From: test@test.com\r\nTo: bob@test.com\r\nSubject: Test\r\n\r\nHello, World!"
	_, err = us.Deliver(bytes.NewReader([]byte(msgContent)))
	if err != nil {
		t.Fatalf("Deliver() = %v", err)
	}

	// Check that there's a file in new/
	newDir := filepath.Join(dir, "bob", "new")
	entries, err := os.ReadDir(newDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Error("no messages in new/ after delivery")
	}
}

func TestDeliverMultipleUsers(t *testing.T) {
	dir, err := os.MkdirTemp("", "maildir-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	m := New(dir)
	msgContent := "From: test@test.com\r\nTo: user@test.com\r\nSubject: Test\r\n\r\nMulti deliver test"

	for _, user := range []string{"alice", "bob", "carol"} {
		us, err := m.ForUser(user)
		if err != nil {
			t.Fatal(err)
		}
		_, err = us.Deliver(bytes.NewReader([]byte(msgContent)))
		if err != nil {
			t.Errorf("Deliver to %s: %v", user, err)
		}
	}

	for _, user := range []string{"alice", "bob", "carol"} {
		newDir := filepath.Join(dir, user, "new")
		entries, _ := os.ReadDir(newDir)
		if len(entries) != 1 {
			t.Errorf("user %s has %d messages, want 1", user, len(entries))
		}
	}
}

func TestDeliverEmptyMessage(t *testing.T) {
	dir, err := os.MkdirTemp("", "maildir-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	m := New(dir)
	us, err := m.ForUser("empty")
	if err != nil {
		t.Fatal(err)
	}

	_, err = us.Deliver(bytes.NewReader([]byte{}))
	if err != nil {
		t.Fatalf("Deliver empty message: %v", err)
	}

	newDir := filepath.Join(dir, "empty", "new")
	entries, _ := os.ReadDir(newDir)
	if len(entries) != 1 {
		t.Errorf("expected 1 message, got %d", len(entries))
	}
}

func TestDeliverLargeMessage(t *testing.T) {
	dir, err := os.MkdirTemp("", "maildir-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	m := New(dir)
	us, err := m.ForUser("large")
	if err != nil {
		t.Fatal(err)
	}

	body := strings.Repeat("A", 100*1024)
	msg := "From: test@test.com\r\nSubject: Large\r\n\r\n" + body

	_, err = us.Deliver(bytes.NewReader([]byte(msg)))
	if err != nil {
		t.Fatalf("Deliver large message: %v", err)
	}

	newDir := filepath.Join(dir, "large", "new")
	entries, _ := os.ReadDir(newDir)
	if len(entries) != 1 {
		t.Errorf("expected 1 message, got %d", len(entries))
	}

	info, _ := entries[0].Info()
	if info.Size() < 100*1024 {
		t.Errorf("message size %d, want at least %d", info.Size(), 100*1024)
	}
}

func TestDeliverConcurrent(t *testing.T) {
	dir, err := os.MkdirTemp("", "maildir-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	m := New(dir)
	us, err := m.ForUser("concurrent")
	if err != nil {
		t.Fatal(err)
	}

	n := 10
	errc := make(chan error, n)
	for i := 0; i < n; i++ {
		go func(id int) {
			msg := fmt.Sprintf("Subject: Message %d\r\n\r\nBody %d", id, id)
			_, err := us.Deliver(bytes.NewReader([]byte(msg)))
			errc <- err
		}(i)
	}

	for i := 0; i < n; i++ {
		if err := <-errc; err != nil {
			t.Errorf("concurrent deliver: %v", err)
		}
	}

	newDir := filepath.Join(dir, "concurrent", "new")
	entries, _ := os.ReadDir(newDir)
	if len(entries) != n {
		t.Errorf("expected %d messages, got %d", n, len(entries))
	}
}

func TestDeliverDouble(t *testing.T) {
	dir, err := os.MkdirTemp("", "maildir-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	m := New(dir)
	us, err := m.ForUser("double")
	if err != nil {
		t.Fatal(err)
	}

	msg := "Subject: Same\r\n\r\nBody"
	for i := 0; i < 2; i++ {
		_, err := us.Deliver(bytes.NewReader([]byte(msg)))
		if err != nil {
			t.Fatalf("Deliver #%d: %v", i, err)
		}
	}

	newDir := filepath.Join(dir, "double", "new")
	entries, _ := os.ReadDir(newDir)
	if len(entries) != 2 {
		t.Errorf("expected 2 messages, got %d", len(entries))
	}
}
