package delivery

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"
)

type Outbound struct {
	hostname string
}

func New(hostname string) *Outbound {
	return &Outbound{hostname: hostname}
}

func (d *Outbound) Deliver(from string, to []string, data []byte) error {
	for _, rcpt := range to {
		domain, err := extractDomain(rcpt)
		if err != nil {
			return fmt.Errorf("extract domain from %s: %w", rcpt, err)
		}

		mx, err := lookupMX(domain)
		if err != nil {
			return fmt.Errorf("lookup MX for %s: %w", domain, err)
		}

		if err := deliverToMX(mx, d.hostname, from, []string{rcpt}, data); err != nil {
			return fmt.Errorf("deliver to %s via %s: %w", rcpt, mx, err)
		}
	}
	return nil
}

func extractDomain(addr string) (string, error) {
	parts := strings.Split(addr, "@")
	if len(parts) != 2 || parts[1] == "" {
		return "", fmt.Errorf("invalid email address: %s", addr)
	}
	return strings.ToLower(parts[1]), nil
}

func lookupMX(domain string) (string, error) {
	mxs, err := net.LookupMX(domain)
	if err != nil || len(mxs) == 0 {
		host, e := net.LookupHost(domain)
		if e != nil || len(host) == 0 {
			return "", fmt.Errorf("no MX or A record for %s", domain)
		}
		return fmt.Sprintf("%s:25", domain), nil
	}
	return fmt.Sprintf("%s:25", strings.TrimSuffix(mxs[0].Host, ".")), nil
}

func deliverToMX(mxAddr, heloName, from string, to []string, data []byte) error {
	conn, err := net.DialTimeout("tcp", mxAddr, 30*time.Second)
	if err != nil {
		return fmt.Errorf("connect to %s: %w", mxAddr, err)
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(60 * time.Second))

	client, err := smtp.NewClient(conn, mxAddr)
	if err != nil {
		return fmt.Errorf("SMTP client to %s: %w", mxAddr, err)
	}
	defer client.Close()

	if err := client.Hello(heloName); err != nil {
		return fmt.Errorf("HELO %s: %w", heloName, err)
	}

	// Opportunistic STARTTLS: upgrade to TLS when the remote MX advertises it.
	// The MX hostname rarely matches its certificate, so verification is left
	// disabled (standard practice for opportunistic MTA-to-MTA TLS); it still
	// protects the message from passive eavesdropping in transit.
	if ok, _ := client.Extension("STARTTLS"); ok {
		host := mxAddr
		if h, _, err := net.SplitHostPort(mxAddr); err == nil {
			host = h
		}
		tlsConfig := &tls.Config{ServerName: host, InsecureSkipVerify: true}
		if err := client.StartTLS(tlsConfig); err != nil {
			return fmt.Errorf("STARTTLS to %s: %w", mxAddr, err)
		}
	}

	if err := client.Mail(from); err != nil {
		return fmt.Errorf("MAIL FROM <%s>: %w", from, err)
	}

	for _, rcpt := range to {
		if err := client.Rcpt(rcpt); err != nil {
			return fmt.Errorf("RCPT TO <%s>: %w", rcpt, err)
		}
	}

	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("DATA: %w", err)
	}

	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("write body: %w", err)
	}

	if err := w.Close(); err != nil {
		return fmt.Errorf("close body: %w", err)
	}

	if err := client.Quit(); err != nil {
		return fmt.Errorf("QUIT: %w", err)
	}

	return nil
}
