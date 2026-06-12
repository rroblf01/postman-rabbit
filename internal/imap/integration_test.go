package imap

import (
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/rroblf01/postman-rabbit/internal/auth"
	"github.com/rroblf01/postman-rabbit/internal/storage"
)

func newTestServer(t *testing.T) (string, *storage.Manager, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "imap-integration-*")
	if err != nil {
		t.Fatal(err)
	}
	store := storage.New(dir)
	authMgr := auth.New(map[string]string{
		"testuser": "testpass",
	})

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		os.RemoveAll(dir)
		t.Fatal(err)
	}

	srv := imapserver.New(&imapserver.Options{
		NewSession: func(c *imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return NewSession(authMgr, store), nil, nil
		},
		Caps: imap.CapSet{
			imap.CapIMAP4rev2: {},
			imap.CapNamespace: {},
			imap.CapMove:      {},
			imap.CapUIDPlus:   {},
		},
		InsecureAuth: true,
	})

	go srv.Serve(lis)
	t.Cleanup(func() {
		srv.Close()
		lis.Close()
		os.RemoveAll(dir)
	})

	return lis.Addr().String(), store, dir
}

func dialIMAP(t *testing.T, addr string) *imapclient.Client {
	t.Helper()

	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	c := imapclient.New(conn, nil)
	if err := c.WaitGreeting(); err != nil {
		t.Fatalf("greeting: %v", err)
	}

	return c
}

func login(t *testing.T, c *imapclient.Client) {
	t.Helper()
	if err := c.Login("testuser", "testpass").Wait(); err != nil {
		t.Fatalf("Login: %v", err)
	}
}

func appendMsg(t *testing.T, c *imapclient.Client, mbox, subject string) imap.UID {
	t.Helper()
	msg := "Subject: " + subject + "\r\n\r\nBody"
	body := strings.NewReader(msg)
	appendCmd := c.Append(mbox, int64(len(msg)), nil)
	body.WriteTo(appendCmd)
	appendCmd.Close()
	ad, err := appendCmd.Wait()
	if err != nil {
		t.Fatalf("Append(%q): %v", subject, err)
	}
	return ad.UID
}

func TestIMAPIntegration_LoginAndCapability(t *testing.T) {
	addr, _, _ := newTestServer(t)
	c := dialIMAP(t, addr)
	defer c.Close()

	caps := c.Caps()
	if !caps.Has(imap.CapIMAP4rev2) {
		t.Error("missing IMAP4rev2 capability")
	}

	login(t, c)
}

func TestIMAPIntegration_Unauthenticated(t *testing.T) {
	addr, _, _ := newTestServer(t)
	c := dialIMAP(t, addr)
	defer c.Close()

	err := c.Login("testuser", "wrongpass").Wait()
	if err == nil {
		t.Fatal("Login with wrong password should fail")
	}
}

func TestIMAPIntegration_AppendAndFetch(t *testing.T) {
	addr, _, _ := newTestServer(t)
	c := dialIMAP(t, addr)
	defer c.Close()

	login(t, c)

	msg := "From: alice@test.com\r\nTo: bob@test.com\r\nSubject: Hello\r\n\r\nThis is a test message."
	appendCmd := c.Append("INBOX", int64(len(msg)), nil)
	strings.NewReader(msg).WriteTo(appendCmd)
	appendCmd.Close()
	appendData, err := appendCmd.Wait()
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if appendData.UID == 0 {
		t.Error("Append UID should not be 0")
	}

	selData, err := c.Select("INBOX", nil).Wait()
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if selData.NumMessages != 1 {
		t.Errorf("NumMessages = %d, want 1", selData.NumMessages)
	}

	fetchCmd := c.Fetch(imap.UIDSet{
		{Start: appendData.UID, Stop: appendData.UID},
	}, &imap.FetchOptions{
		Flags:        true,
		InternalDate: true,
		RFC822Size:   true,
		Envelope:     true,
		BodySection: []*imap.FetchItemBodySection{
			{Specifier: imap.PartSpecifierHeader},
			{},
		},
		BodyStructure: &imap.FetchItemBodyStructure{},
	})
	msgs, err := fetchCmd.Collect()
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	m := msgs[0]
	if m.UID != appendData.UID {
		t.Errorf("UID = %d, want %d", m.UID, appendData.UID)
	}
	if m.SeqNum != 1 {
		t.Errorf("SeqNum = %d, want 1", m.SeqNum)
	}
	if m.RFC822Size <= 0 {
		t.Error("RFC822Size should be > 0")
	}
	if m.Envelope == nil {
		t.Error("Envelope should not be nil")
	} else if m.Envelope.Subject != "Hello" {
		t.Errorf("Subject = %q, want %q", m.Envelope.Subject, "Hello")
	}
	if m.BodyStructure == nil {
		t.Error("BodyStructure should not be nil")
	}

	headerSection := m.FindBodySection(&imap.FetchItemBodySection{
		Specifier: imap.PartSpecifierHeader,
	})
	if len(headerSection) == 0 {
		t.Error("header section should not be empty")
	} else if !strings.Contains(string(headerSection), "Subject: Hello") {
		t.Errorf("header should contain Subject: Hello, got %q", string(headerSection))
	}

	bodySection := m.FindBodySection(&imap.FetchItemBodySection{})
	if len(bodySection) == 0 {
		t.Error("body section should not be empty")
	} else if !strings.Contains(string(bodySection), "This is a test message") {
		t.Errorf("body should contain message text, got %q", string(bodySection))
	}
}

func TestIMAPIntegration_StoreFlags(t *testing.T) {
	addr, _, _ := newTestServer(t)
	c := dialIMAP(t, addr)
	defer c.Close()

	login(t, c)

	uid := appendMsg(t, c, "INBOX", "Test")

	c.Select("INBOX", nil).Wait()

	// Add \Seen and \Flagged
	c.Store(imap.UIDSet{{Start: uid, Stop: uid}}, &imap.StoreFlags{
		Op:    imap.StoreFlagsAdd,
		Flags: []imap.Flag{imap.FlagSeen, imap.FlagFlagged},
	}, nil).Collect()

	fetchCmd := c.Fetch(imap.UIDSet{{Start: uid, Stop: uid}}, &imap.FetchOptions{Flags: true})
	msgs, _ := fetchCmd.Collect()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 msg, got %d", len(msgs))
	}

	hasSeen := false
	hasFlagged := false
	for _, f := range msgs[0].Flags {
		if f == imap.FlagSeen {
			hasSeen = true
		}
		if f == imap.FlagFlagged {
			hasFlagged = true
		}
	}
	if !hasSeen {
		t.Error("expected \\Seen flag")
	}
	if !hasFlagged {
		t.Error("expected \\Flagged flag")
	}

	// Remove \Flagged
	c.Store(imap.UIDSet{{Start: uid, Stop: uid}}, &imap.StoreFlags{
		Op:    imap.StoreFlagsDel,
		Flags: []imap.Flag{imap.FlagFlagged},
	}, nil).Collect()

	fetchCmd = c.Fetch(imap.UIDSet{{Start: uid, Stop: uid}}, &imap.FetchOptions{Flags: true})
	msgs, _ = fetchCmd.Collect()
	hasFlagged = false
	for _, f := range msgs[0].Flags {
		if f == imap.FlagFlagged {
			hasFlagged = true
		}
	}
	if hasFlagged {
		t.Error("\\Flagged should have been removed")
	}

	// Set only \Draft (replace all)
	c.Store(imap.UIDSet{{Start: uid, Stop: uid}}, &imap.StoreFlags{
		Op:    imap.StoreFlagsSet,
		Flags: []imap.Flag{imap.FlagDraft},
	}, nil).Collect()

	fetchCmd = c.Fetch(imap.UIDSet{{Start: uid, Stop: uid}}, &imap.FetchOptions{Flags: true})
	msgs, _ = fetchCmd.Collect()
	if len(msgs[0].Flags) != 1 || msgs[0].Flags[0] != imap.FlagDraft {
		t.Errorf("flags = %v, want [\\Draft]", msgs[0].Flags)
	}
}

func TestIMAPIntegration_Move(t *testing.T) {
	addr, _, _ := newTestServer(t)
	c := dialIMAP(t, addr)
	defer c.Close()

	login(t, c)
	c.Create("INBOX/Archive", nil).Wait()

	uid := appendMsg(t, c, "INBOX", "Move Me")
	c.Select("INBOX", nil).Wait()

	moveCmd := c.Move(imap.UIDSet{{Start: uid, Stop: uid}}, "INBOX/Archive")
	if _, err := moveCmd.Wait(); err != nil {
		t.Fatalf("Move: %v", err)
	}

	selData, _ := c.Select("INBOX", nil).Wait()
	if selData.NumMessages != 0 {
		t.Errorf("INBOX NumMessages after move = %d, want 0", selData.NumMessages)
	}

	selData, _ = c.Select("INBOX/Archive", nil).Wait()
	if selData.NumMessages != 1 {
		t.Errorf("Archive NumMessages after move = %d, want 1", selData.NumMessages)
	}
}

func TestIMAPIntegration_Copy(t *testing.T) {
	addr, _, _ := newTestServer(t)
	c := dialIMAP(t, addr)
	defer c.Close()

	login(t, c)
	c.Create("INBOX/Backup", nil).Wait()

	uid := appendMsg(t, c, "INBOX", "Copy Me")
	c.Select("INBOX", nil).Wait()

	copyCmd := c.Copy(imap.UIDSet{{Start: uid, Stop: uid}}, "INBOX/Backup")
	if _, err := copyCmd.Wait(); err != nil {
		t.Fatalf("Copy: %v", err)
	}

	selData, _ := c.Select("INBOX", nil).Wait()
	if selData.NumMessages != 1 {
		t.Errorf("INBOX NumMessages after copy = %d, want 1", selData.NumMessages)
	}

	selData, _ = c.Select("INBOX/Backup", nil).Wait()
	if selData.NumMessages != 1 {
		t.Errorf("Backup NumMessages after copy = %d, want 1", selData.NumMessages)
	}
}

func TestIMAPIntegration_Expunge(t *testing.T) {
	addr, _, _ := newTestServer(t)
	c := dialIMAP(t, addr)
	defer c.Close()

	login(t, c)

	uid1 := appendMsg(t, c, "INBOX", "Msg 1")
	uid2 := appendMsg(t, c, "INBOX", "Msg 2")
	appendMsg(t, c, "INBOX", "Msg 3")

	c.Select("INBOX", nil).Wait()

	c.Store(imap.UIDSet{{Start: uid1, Stop: uid1}}, &imap.StoreFlags{
		Op:    imap.StoreFlagsAdd,
		Flags: []imap.Flag{imap.FlagDeleted},
	}, nil).Collect()
	c.Store(imap.UIDSet{{Start: uid2, Stop: uid2}}, &imap.StoreFlags{
		Op:    imap.StoreFlagsAdd,
		Flags: []imap.Flag{imap.FlagDeleted},
	}, nil).Collect()

	expungeCmd := c.Expunge()
	expunged, err := expungeCmd.Collect()
	if err != nil {
		t.Fatalf("Expunge: %v", err)
	}
	if len(expunged) != 2 {
		t.Errorf("expunged %d messages, want 2", len(expunged))
	}

	selData, _ := c.Select("INBOX", nil).Wait()
	if selData.NumMessages != 1 {
		t.Errorf("NumMessages after expunge = %d, want 1", selData.NumMessages)
	}
}

func TestIMAPIntegration_Idle(t *testing.T) {
	addr, _, _ := newTestServer(t)
	c := dialIMAP(t, addr)
	defer c.Close()

	login(t, c)
	c.Select("INBOX", nil).Wait()

	idleCmd, err := c.Idle()
	if err != nil {
		t.Fatalf("Idle start: %v", err)
	}

	if err := idleCmd.Close(); err != nil {
		t.Errorf("Idle stop: %v", err)
	}
}

func TestIMAPIntegration_List(t *testing.T) {
	addr, _, _ := newTestServer(t)
	c := dialIMAP(t, addr)
	defer c.Close()

	login(t, c)

	listCmd := c.List("", "%", nil)
	listData, err := listCmd.Collect()
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	foundINBOX := false
	for _, l := range listData {
		if l.Mailbox == "INBOX" {
			foundINBOX = true
		}
	}
	if !foundINBOX {
		t.Errorf("List should include INBOX, got %v", mailboxesFromList(listData))
	}
}

func TestIMAPIntegration_ListWithMailboxes(t *testing.T) {
	addr, _, _ := newTestServer(t)
	c := dialIMAP(t, addr)
	defer c.Close()

	login(t, c)
	c.Create("INBOX/Archive", nil).Wait()

	listCmd := c.List("", "*", nil)
	listData, err := listCmd.Collect()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	boxes := mailboxesFromList(listData)

	if !contains(boxes, "INBOX") {
		t.Errorf("expected INBOX, got %v", boxes)
	}
	if !contains(boxes, "INBOX/Archive") {
		t.Errorf("expected INBOX/Archive, got %v", boxes)
	}
}

func TestIMAPIntegration_Namespace(t *testing.T) {
	addr, _, _ := newTestServer(t)
	c := dialIMAP(t, addr)
	defer c.Close()

	login(t, c)

	nsCmd := c.Namespace()
	ns, err := nsCmd.Wait()
	if err != nil {
		t.Fatalf("Namespace: %v", err)
	}
	if len(ns.Personal) == 0 {
		t.Fatal("Personal namespace should not be empty")
	}
}

func TestIMAPIntegration_Status(t *testing.T) {
	addr, _, _ := newTestServer(t)
	c := dialIMAP(t, addr)
	defer c.Close()

	login(t, c)
	appendMsg(t, c, "INBOX", "Msg 1")
	appendMsg(t, c, "INBOX", "Msg 2")

	statusCmd := c.Status("INBOX", &imap.StatusOptions{
		NumMessages: true,
		NumUnseen:   true,
		UIDNext:     true,
		UIDValidity: true,
	})
	statusData, err := statusCmd.Wait()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if *statusData.NumMessages != 2 {
		t.Errorf("NumMessages = %d, want 2", *statusData.NumMessages)
	}
	if *statusData.NumUnseen != 2 {
		t.Errorf("NumUnseen = %d, want 2", *statusData.NumUnseen)
	}
}

func TestIMAPIntegration_AppendWithFlags(t *testing.T) {
	addr, _, _ := newTestServer(t)
	c := dialIMAP(t, addr)
	defer c.Close()

	login(t, c)

	msg := "Subject: Seen\r\n\r\nBody"
	appendCmd := c.Append("INBOX", int64(len(msg)), &imap.AppendOptions{
		Flags: []imap.Flag{imap.FlagSeen},
	})
	strings.NewReader(msg).WriteTo(appendCmd)
	appendCmd.Close()
	ad, err := appendCmd.Wait()
	if err != nil {
		t.Fatalf("Append: %v", err)
	}

	c.Select("INBOX", nil).Wait()

	fetchCmd := c.Fetch(imap.UIDSet{{Start: ad.UID, Stop: ad.UID}}, &imap.FetchOptions{Flags: true})
	msgs, _ := fetchCmd.Collect()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 msg, got %d", len(msgs))
	}
	hasSeen := false
	for _, f := range msgs[0].Flags {
		if f == imap.FlagSeen {
			hasSeen = true
		}
	}
	if !hasSeen {
		t.Error("message appended with \\Seen flag should have it")
	}
}

func TestIMAPIntegration_CreateDeleteRename(t *testing.T) {
	addr, _, _ := newTestServer(t)
	c := dialIMAP(t, addr)
	defer c.Close()

	login(t, c)
	c.Create("INBOX/Work", nil).Wait()

	listCmd := c.List("", "*", nil)
	listData, _ := listCmd.Collect()
	if !contains(mailboxesFromList(listData), "INBOX/Work") {
		t.Error("INBOX/Work should exist after create")
	}

	c.Rename("INBOX/Work", "INBOX/Personal", nil).Wait()
	listCmd = c.List("", "*", nil)
	listData, _ = listCmd.Collect()
	boxes := mailboxesFromList(listData)
	if contains(boxes, "INBOX/Work") {
		t.Error("INBOX/Work should not exist after rename")
	}
	if !contains(boxes, "INBOX/Personal") {
		t.Error("INBOX/Personal should exist after rename")
	}

	appendMsg(t, c, "INBOX/Personal", "Test")
	selData, _ := c.Select("INBOX/Personal", nil).Wait()
	if selData.NumMessages != 1 {
		t.Errorf("Personal NumMessages = %d, want 1", selData.NumMessages)
	}

	c.Delete("INBOX/Personal").Wait()
	listCmd = c.List("", "*", nil)
	listData, _ = listCmd.Collect()
	if contains(mailboxesFromList(listData), "INBOX/Personal") {
		t.Error("INBOX/Personal should not exist after delete")
	}
}

func mailboxesFromList(list []*imap.ListData) []string {
	var out []string
	for _, l := range list {
		if l.Mailbox != "" {
			out = append(out, l.Mailbox)
		}
	}
	return out
}

func contains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}
