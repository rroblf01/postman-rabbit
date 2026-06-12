package storage

import (
	"bytes"
	"os"
	"path/filepath"
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
