package smtp

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"strings"
	"time"

	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"
	"github.com/rroblf01/postman-rabbit/internal/auth"
	"github.com/rroblf01/postman-rabbit/internal/delivery"
	"github.com/rroblf01/postman-rabbit/internal/dkim"
	"github.com/rroblf01/postman-rabbit/internal/storage"
)

type Backend struct {
	auth         *auth.Manager
	storage      *storage.Manager
	delivery     *delivery.Outbound
	dkim         *dkim.Signer
	hostname     string
	domain       string
	relayEnabled bool
}

func NewBackend(authMgr *auth.Manager, store *storage.Manager, del *delivery.Outbound, dkimSigner *dkim.Signer, hostname, domain string, relayEnabled bool) *Backend {
	return &Backend{
		auth:         authMgr,
		storage:      store,
		delivery:     del,
		dkim:         dkimSigner,
		hostname:     hostname,
		domain:       domain,
		relayEnabled: relayEnabled,
	}
}

func (b *Backend) NewSession(c *smtp.Conn) (smtp.Session, error) {
	return &Session{
		backend: b,
	}, nil
}

type Session struct {
	backend       *Backend
	username      string
	authenticated bool
	from          string
	recipients    []string
}

func (s *Session) AuthMechanisms() []string {
	return []string{"PLAIN", "LOGIN"}
}

func (s *Session) Auth(mech string) (sasl.Server, error) {
	switch strings.ToUpper(mech) {
	case "PLAIN":
		return sasl.NewPlainServer(func(identity, username, password string) error {
			if s.backend.auth.Authenticate(username, password) {
				s.username = username
				s.authenticated = true
				return nil
			}
			return fmt.Errorf("authentication failed")
		}), nil
	case "LOGIN":
		return &loginServer{
			auth: s.backend.auth,
			sess: s,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported mechanism")
	}
}

func (s *Session) Mail(from string, opts *smtp.MailOptions) error {
	s.from = from
	s.recipients = nil
	return nil
}

func (s *Session) Rcpt(to string, opts *smtp.RcptOptions) error {
	s.recipients = append(s.recipients, to)
	return nil
}

func (s *Session) Data(r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("read message: %w", err)
	}

	localRcpts, remoteRcpts := s.partitionRecipients()

	if len(localRcpts) > 0 {
		if err := s.deliverLocal(localRcpts, data); err != nil {
			return err
		}
	}

	if len(remoteRcpts) > 0 {
		if !s.authenticated {
			log.Printf("relay denied for unauthenticated session from=%s", s.from)
			return &smtp.SMTPError{Code: 550, EnhancedCode: smtp.EnhancedCode{5, 7, 1}, Message: "relay not allowed: authentication required"}
		}
		if !s.backend.relayEnabled {
			log.Printf("relay disabled, denying remote delivery from=%s", s.from)
			return &smtp.SMTPError{Code: 550, EnhancedCode: smtp.EnhancedCode{5, 7, 1}, Message: "relaying is disabled on this server"}
		}

		outgoing := s.backend.ensureHeaders(data)
		signed, err := s.backend.dkim.Sign(outgoing)
		if err != nil {
			log.Printf("DKIM sign failed, sending unsigned: %v", err)
			signed = outgoing
		}
		if err := s.backend.delivery.Deliver(s.from, remoteRcpts, signed); err != nil {
			log.Printf("delivery error: %v", err)
			return &smtp.SMTPError{Code: 451, EnhancedCode: smtp.EnhancedCode{4, 4, 0}, Message: "temporary delivery failure, try again later"}
		}
	}

	return nil
}

// ensureHeaders guarantees the outgoing message has Date and Message-ID headers,
// which are required for good deliverability and stable DKIM signing. Existing
// headers are left untouched.
func (b *Backend) ensureHeaders(data []byte) []byte {
	headerEnd := bytes.Index(data, []byte("\r\n\r\n"))
	var headerBlock string
	if headerEnd >= 0 {
		headerBlock = strings.ToLower(string(data[:headerEnd]))
	} else {
		headerBlock = strings.ToLower(string(data))
	}

	var prepend bytes.Buffer
	if !strings.Contains(headerBlock, "\nmessage-id:") && !strings.HasPrefix(headerBlock, "message-id:") {
		fmt.Fprintf(&prepend, "Message-ID: <%s@%s>\r\n", randomToken(), b.hostname)
	}
	if !strings.Contains(headerBlock, "\ndate:") && !strings.HasPrefix(headerBlock, "date:") {
		fmt.Fprintf(&prepend, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	}
	if prepend.Len() == 0 {
		return data
	}
	prepend.Write(data)
	return prepend.Bytes()
}

func randomToken() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// rand.Read failing is effectively impossible; fall back to a timestamp.
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf[:])
}

func (s *Session) Reset() {
	s.from = ""
	s.recipients = nil
}

func (s *Session) Logout() error {
	return nil
}

func (s *Session) partitionRecipients() (local, remote []string) {
	localDomain := strings.ToLower(s.backend.domain)
	for _, rcpt := range s.recipients {
		parts := strings.Split(rcpt, "@")
		if len(parts) == 2 {
			domain := strings.ToLower(parts[1])
			user := parts[0]
			if domain == localDomain && s.backend.auth.Exists(user) {
				local = append(local, user)
			} else {
				remote = append(remote, rcpt)
			}
		}
	}
	return
}

func (s *Session) deliverLocal(users []string, data []byte) error {
	var failed []string
	for _, user := range users {
		us, err := s.backend.storage.ForUser(user)
		if err != nil {
			log.Printf("storage for %s: %v", user, err)
			failed = append(failed, user)
			continue
		}
		if _, err := us.Deliver(bytes.NewReader(data)); err != nil {
			log.Printf("deliver to %s: %v", user, err)
			failed = append(failed, user)
			continue
		}
		log.Printf("delivered to %s", user)
	}
	if len(failed) > 0 {
		// Returning a temporary error makes the sending MTA retry rather than
		// silently dropping the message.
		return &smtp.SMTPError{Code: 451, EnhancedCode: smtp.EnhancedCode{4, 2, 0}, Message: "temporary failure storing message, try again later"}
	}
	return nil
}

type loginServer struct {
	auth *auth.Manager
	sess *Session
	step int
}

func (s *loginServer) Next(response []byte) (challenge []byte, done bool, err error) {
	switch s.step {
	case 0:
		s.step++
		return []byte("Username:"), false, nil
	case 1:
		s.step++
		s.sess.username = string(response)
		return []byte("Password:"), false, nil
	case 2:
		if s.auth.Authenticate(s.sess.username, string(response)) {
			s.sess.authenticated = true
			return nil, true, nil
		}
		return nil, true, fmt.Errorf("authentication failed")
	default:
		return nil, true, fmt.Errorf("authentication failed")
	}
}

var _ smtp.AuthSession = (*Session)(nil)
