package imap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/rroblf01/postman-rabbit/internal/auth"
	"github.com/rroblf01/postman-rabbit/internal/storage"
)

func newTestSession(t *testing.T) (imapserver.Session, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "imap-test-*")
	if err != nil {
		t.Fatal(err)
	}
	store := storage.New(dir)
	authMgr := auth.New(map[string]string{
		"testuser": "testpass",
	})
	sess := NewSession(authMgr, store)
	return sess, dir
}

func TestLogin(t *testing.T) {
	sess, cleanup := newTestSession(t)
	defer os.RemoveAll(cleanup)

	if err := sess.Login("testuser", "testpass"); err != nil {
		t.Fatalf("Login() = %v", err)
	}

	if err := sess.Login("testuser", "wrong"); err == nil {
		t.Error("Login with wrong password should fail")
	}

	if err := sess.Login("unknown", "pass"); err == nil {
		t.Error("Login with unknown user should fail")
	}
}

func TestClose(t *testing.T) {
	sess, cleanup := newTestSession(t)
	defer os.RemoveAll(cleanup)

	if err := sess.Close(); err != nil {
		t.Errorf("Close() = %v", err)
	}
}

func TestCreateAndSelect(t *testing.T) {
	sess, cleanup := newTestSession(t)
	defer os.RemoveAll(cleanup)

	sess.Login("testuser", "testpass")

	if err := sess.Create("INBOX/Work", nil); err != nil {
		t.Fatalf("Create() = %v", err)
	}
	if err := sess.Create("INBOX/Personal", nil); err != nil {
		t.Fatalf("Create() = %v", err)
	}

	data, err := sess.Select("INBOX/Work", nil)
	if err != nil {
		t.Fatalf("Select() = %v", err)
	}
	if data == nil {
		t.Fatal("Select returned nil")
	}
}

func TestSelect(t *testing.T) {
	sess, cleanup := newTestSession(t)
	defer os.RemoveAll(cleanup)

	sess.Login("testuser", "testpass")

	data, err := sess.Select("INBOX", nil)
	if err != nil {
		t.Fatalf("Select() = %v", err)
	}
	if data == nil {
		t.Fatal("Select returned nil data")
	}
	if data.NumMessages != 0 {
		t.Errorf("NumMessages = %d, want 0", data.NumMessages)
	}
	if data.UIDValidity == 0 {
		t.Error("UIDValidity should not be 0")
	}

	if _, err := sess.Select("INBOX/Nope", nil); err == nil {
		t.Error("Select non-existent should fail")
	}
}

func TestUnselect(t *testing.T) {
	sess, cleanup := newTestSession(t)
	defer os.RemoveAll(cleanup)

	sess.Login("testuser", "testpass")
	sess.Select("INBOX", nil)

	if err := sess.Unselect(); err != nil {
		t.Errorf("Unselect() = %v", err)
	}
}

func TestStatus(t *testing.T) {
	sess, cleanup := newTestSession(t)
	defer os.RemoveAll(cleanup)

	sess.Login("testuser", "testpass")

	data, err := sess.Status("INBOX", &imap.StatusOptions{
		NumMessages: true,
		NumUnseen:   true,
		UIDNext:     true,
		UIDValidity: true,
	})
	if err != nil {
		t.Fatalf("Status() = %v", err)
	}
	if data == nil {
		t.Fatal("Status returned nil")
	}
	if *data.NumMessages != 0 {
		t.Errorf("NumMessages = %d, want 0", *data.NumMessages)
	}
	if *data.NumUnseen != 0 {
		t.Errorf("NumUnseen = %d, want 0", *data.NumUnseen)
	}
}

func TestRename(t *testing.T) {
	sess, cleanup := newTestSession(t)
	defer os.RemoveAll(cleanup)

	sess.Login("testuser", "testpass")
	sess.Create("INBOX/OldName", nil)

	if err := sess.Rename("INBOX/OldName", "INBOX/NewName", nil); err != nil {
		t.Fatalf("Rename() = %v", err)
	}

	if _, err := sess.Select("INBOX/NewName", nil); err != nil {
		t.Error("Renamed mailbox should exist")
	}
}

func TestDelete(t *testing.T) {
	sess, cleanup := newTestSession(t)
	defer os.RemoveAll(cleanup)

	sess.Login("testuser", "testpass")
	sess.Create("INBOX/Trash", nil)

	if err := sess.Delete("INBOX/Trash"); err != nil {
		t.Fatalf("Delete() = %v", err)
	}
}

func TestSubscribeUnsubscribe(t *testing.T) {
	sess, cleanup := newTestSession(t)
	defer os.RemoveAll(cleanup)

	sess.Login("testuser", "testpass")

	if err := sess.Subscribe("INBOX"); err != nil {
		t.Errorf("Subscribe() = %v", err)
	}
	if err := sess.Unsubscribe("INBOX"); err != nil {
		t.Errorf("Unsubscribe() = %v", err)
	}
}

func TestAppend(t *testing.T) {
	sess, cleanup := newTestSession(t)
	defer os.RemoveAll(cleanup)

	sess.Login("testuser", "testpass")

	msg := "From: test@test.com\r\nTo: user@test.com\r\nSubject: Test\r\n\r\nHello!"
	r := strings.NewReader(msg)
	data, err := sess.Append("INBOX", r, nil)
	if err != nil {
		t.Fatalf("Append() = %v", err)
	}
	if data == nil {
		t.Fatal("Append returned nil")
	}
	if data.UID == 0 {
		t.Error("Append UID should not be 0")
	}
}

func TestCopy(t *testing.T) {
	sess, cleanup := newTestSession(t)
	defer os.RemoveAll(cleanup)

	sess.Login("testuser", "testpass")
	sess.Create("INBOX/Backup", nil)

	msg := "From: test@test.com\r\nSubject: Copy\r\n\r\nBody"
	r := strings.NewReader(msg)
	appendData, err := sess.Append("INBOX", r, nil)
	if err != nil {
		t.Fatal(err)
	}

	sess.Select("INBOX", nil)
	uidSet := imap.UIDSet{{Start: appendData.UID, Stop: appendData.UID}}
	copyData, err := sess.Copy(uidSet, "INBOX/Backup")
	if err != nil {
		t.Fatalf("Copy() = %v", err)
	}
	if copyData == nil {
		t.Fatal("Copy returned nil")
	}
}

func TestSearch(t *testing.T) {
	sess, cleanup := newTestSession(t)
	defer os.RemoveAll(cleanup)

	sess.Login("testuser", "testpass")

	msg := "From: test@test.com\r\nSubject: Test\r\n\r\nBody"
	r := strings.NewReader(msg)
	sess.Append("INBOX", r, nil)

	sess.Select("INBOX", nil)
	data, err := sess.Search(imapserver.NumKindUID, nil, nil)
	if err != nil {
		t.Fatalf("Search() = %v", err)
	}
	if data == nil {
		t.Fatal("Search returned nil")
	}
}

// TestSearchCriteria verifies SEARCH honours the criteria instead of always
// returning every message.
func TestSearchCriteria(t *testing.T) {
	sess, cleanup := newTestSession(t)
	defer os.RemoveAll(cleanup)

	sess.Login("testuser", "testpass")

	// One seen, one unseen.
	seenUID := mustAppendUID(t, sess, "INBOX", "Subject: Seen\r\n\r\nhello world", []imap.Flag{imap.FlagSeen})
	unseenUID := mustAppendUID(t, sess, "INBOX", "Subject: Unseen\r\n\r\ngoodbye", nil)
	sess.Select("INBOX", nil)

	// UNSEEN -> only the unseen message.
	res, err := sess.Search(imapserver.NumKindUID, &imap.SearchCriteria{
		NotFlag: []imap.Flag{imap.FlagSeen},
	}, nil)
	if err != nil {
		t.Fatalf("Search(UNSEEN) = %v", err)
	}
	uids, _ := res.All.(imap.UIDSet)
	if !uids.Contains(unseenUID) || uids.Contains(seenUID) {
		t.Errorf("UNSEEN search = %v, want only uid %d", uids, unseenUID)
	}

	// BODY "world" -> only the first message.
	res, err = sess.Search(imapserver.NumKindUID, &imap.SearchCriteria{
		Body: []string{"world"},
	}, nil)
	if err != nil {
		t.Fatalf("Search(BODY) = %v", err)
	}
	uids, _ = res.All.(imap.UIDSet)
	if !uids.Contains(seenUID) || uids.Contains(unseenUID) {
		t.Errorf("BODY search = %v, want only uid %d", uids, seenUID)
	}
}

// TestUIDsMonotonic verifies UIDs are assigned in ascending order and stay
// stable after a flag change (which renames the maildir file).
func TestUIDsMonotonic(t *testing.T) {
	sess, cleanup := newTestSession(t)
	defer os.RemoveAll(cleanup)

	sess.Login("testuser", "testpass")

	uid1 := mustAppendUID(t, sess, "INBOX", "Subject: One\r\n\r\nA", nil)
	uid2 := mustAppendUID(t, sess, "INBOX", "Subject: Two\r\n\r\nB", nil)
	uid3 := mustAppendUID(t, sess, "INBOX", "Subject: Three\r\n\r\nC", nil)

	if !(uid1 < uid2 && uid2 < uid3) {
		t.Fatalf("UIDs not ascending: %d, %d, %d", uid1, uid2, uid3)
	}

	sess.Select("INBOX", nil)

	// Flag the second message \Seen (renames its file); its UID must not change.
	if err := sess.Store(nil, imap.UIDSet{{Start: uid2, Stop: uid2}}, &imap.StoreFlags{
		Op:    imap.StoreFlagsAdd,
		Flags: []imap.Flag{imap.FlagSeen},
	}, nil); err != nil {
		t.Fatalf("Store: %v", err)
	}

	data, err := sess.Search(imapserver.NumKindUID, &imap.SearchCriteria{
		Flag: []imap.Flag{imap.FlagSeen},
	}, nil)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	uids, _ := data.All.(imap.UIDSet)
	if !uids.Contains(uid2) {
		t.Errorf("after flag change, seen search = %v, want to contain stable uid %d", uids, uid2)
	}
}

func mustAppendUID(t *testing.T, sess imapserver.Session, mbox, msg string, flags []imap.Flag) imap.UID {
	t.Helper()
	var opts *imap.AppendOptions
	if len(flags) > 0 {
		opts = &imap.AppendOptions{Flags: flags}
	}
	ad, err := sess.Append(mbox, strings.NewReader(msg), opts)
	if err != nil {
		t.Fatalf("Append(%q): %v", msg, err)
	}
	return ad.UID
}

func TestPoll(t *testing.T) {
	sess, cleanup := newTestSession(t)
	defer os.RemoveAll(cleanup)

	sess.Login("testuser", "testpass")

	if err := sess.Poll(nil, true); err != nil {
		t.Errorf("Poll() = %v", err)
	}
}

func TestNamespace(t *testing.T) {
	sess, cleanup := newTestSession(t)
	defer os.RemoveAll(cleanup)

	sess.Login("testuser", "testpass")

	ns, err := sess.(imapserver.SessionIMAP4rev2).Namespace()
	if err != nil {
		t.Fatalf("Namespace() = %v", err)
	}
	if len(ns.Personal) != 1 {
		t.Errorf("len(Personal) = %d, want 1", len(ns.Personal))
	}
	if ns.Personal[0].Delim != delim {
		t.Errorf("Delim = %q, want %q", ns.Personal[0].Delim, delim)
	}
}

func TestMboxPath(t *testing.T) {
	dir, err := os.MkdirTemp("", "imap-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	store := storage.New(dir)
	authMgr := auth.New(map[string]string{"user": "pass"})
	sess := NewSession(authMgr, store).(*session)
	sess.Login("user", "pass")

	tests := []struct {
		name     string
		expected string
	}{
		{"INBOX", sess.userDir},
		{"INBOX/", sess.userDir},
		{"", sess.userDir},
		{"INBOX/Work", filepath.Join(sess.userDir, "Work")},
		{"INBOX/../etc", sess.userDir},
		{"INBOX/../../../tmp", sess.userDir},
		{"/etc/passwd", sess.userDir},
	}

	for _, tc := range tests {
		got := sess.mboxPath(tc.name)
		if got != tc.expected {
			t.Errorf("mboxPath(%q) = %q, want %q", tc.name, got, tc.expected)
		}
	}
}
