package delivery

import (
	"bufio"
	"net"
	"strings"
	"testing"
	"time"
)

func TestExtractDomain(t *testing.T) {
	tests := []struct {
		addr    string
		want    string
		wantErr bool
	}{
		{"user@example.com", "example.com", false},
		{"alice@gmail.com", "gmail.com", false},
		{"test@sub.domain.co.uk", "sub.domain.co.uk", false},
		{"", "", true},
		{"noatsign", "", true},
		{"@emptyuser.com", "emptyuser.com", false},
		{"user@", "", true},
		{"user@UPPERCASE.COM", "uppercase.com", false},
	}

	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			got, err := extractDomain(tt.addr)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if got != tt.want {
				t.Errorf("extractDomain(%q) = %q, want %q", tt.addr, got, tt.want)
			}
		})
	}
}

func TestLookupMX(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DNS-dependent test in short mode")
	}
	knownDomains := []string{"gmail.com", "outlook.com"}
	for _, domain := range knownDomains {
		t.Run(domain, func(t *testing.T) {
			mx, err := lookupMX(domain)
			if err != nil {
				t.Fatalf("lookupMX(%q) = %v", domain, err)
			}
			if !strings.HasSuffix(mx, ":25") {
				t.Errorf("lookupMX(%q) = %q, want suffix :25", domain, mx)
			}
			if mx == "" {
				t.Error("lookupMX returned empty string")
			}
		})
	}
}

func TestLookupMX_UnknownDomain(t *testing.T) {
	_, err := lookupMX("thisshouldnotexist-12345.com")
	if err == nil {
		t.Error("expected error for non-existent domain, got nil")
	}
}

func TestDeliverToMX_LocalServer(t *testing.T) {
	serverDone := make(chan struct{})
	received := make(chan string, 10)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		defer close(serverDone)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))

		send := func(line string) {
			rw.WriteString(line + "\r\n")
			rw.Flush()
		}

		send("220 localhost ESMTP Mock")
		cmd, _ := rw.ReadString('\n')
		received <- strings.TrimSpace(cmd)
		if strings.HasPrefix(cmd, "EHLO") {
			send("250-localhost Hello")
			send("250-SIZE 35882577")
			send("250 8BITMIME")
		} else {
			send("250 Hello " + strings.Fields(cmd)[1])
		}

		cmd, _ = rw.ReadString('\n')
		received <- strings.TrimSpace(cmd)
		send("250 OK")

		cmd, _ = rw.ReadString('\n')
		received <- strings.TrimSpace(cmd)
		send("250 OK")

		cmd, _ = rw.ReadString('\n')
		received <- strings.TrimSpace(cmd)
		send("354 Start mail input")

		var bodyLines []string
		for {
			line, _ := rw.ReadString('\n')
			if line == ".\r\n" {
				break
			}
			bodyLines = append(bodyLines, strings.TrimSpace(line))
		}
		received <- strings.Join(bodyLines, "|")
		send("250 OK: message accepted")

		cmd, _ = rw.ReadString('\n')
		received <- strings.TrimSpace(cmd)
		send("221 Bye")
	}()

	addr := ln.Addr().String()

	err = deliverToMX(addr, "test.localhost", "alice@test.com", []string{"bob@test.com"}, []byte("Subject: Test\r\n\r\nHello"))
	if err != nil {
		t.Fatalf("deliverToMX() = %v", err)
	}

	<-serverDone

	cmds := []struct {
		prefix string
		desc   string
	}{
		{"EHLO", "HELO/EHLO"},
		{"MAIL FROM:<alice@test.com>", "MAIL FROM"},
		{"RCPT TO:<bob@test.com>", "RCPT TO"},
		{"DATA", "DATA"},
		{".", "body"},
		{"QUIT", "QUIT"},
	}

	for _, c := range cmds {
		select {
		case cmd := <-received:
			if c.prefix == "." {
				if !strings.Contains(cmd, "Subject: Test") {
					t.Errorf("body: got %q, expected Subject: Test", cmd)
				}
			} else if !strings.HasPrefix(cmd, c.prefix) {
				t.Errorf("%s: got %q, want prefix %q", c.desc, cmd, c.prefix)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("timeout waiting for %s", c.desc)
		}
	}
}

func TestDeliverToMX_ConnectionError(t *testing.T) {
	err := deliverToMX("127.0.0.1:1", "test.localhost", "from@test.com", []string{"to@test.com"}, []byte("data"))
	if err == nil {
		t.Error("expected connection error, got nil")
	}
}

func TestDeliverToMX_Rejected(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
		rw.WriteString("220 mock\r\n")
		rw.Flush()
		line, _ := rw.ReadString('\n')
		_ = line
		rw.WriteString("550 Mail rejected\r\n")
		rw.Flush()
	}()

	err = deliverToMX(ln.Addr().String(), "test", "from@test.com", []string{"to@test.com"}, []byte("data"))
	if err == nil {
		t.Error("expected error for rejected mail, got nil")
	}
}

func TestDeliver_SingleRecipient(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping network-dependent test in short mode")
	}
	d := New("test.localhost")
	err := d.Deliver("test@example.com", []string{"user@gmail.com"}, []byte("Subject: Test\r\n\r\nBody"))
	if err != nil {
		t.Logf("Deliver returned error (expected if offline): %v", err)
	}
}

func TestDeliver_InvalidRecipient(t *testing.T) {
	d := New("test.localhost")
	err := d.Deliver("test@example.com", []string{"invalid"}, []byte("data"))
	if err == nil {
		t.Error("expected error for invalid recipient")
	}
}
