package smtp

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"strings"

	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"
	"github.com/rroblf01/postman-rabbit/internal/auth"
	"github.com/rroblf01/postman-rabbit/internal/delivery"
	"github.com/rroblf01/postman-rabbit/internal/dkim"
	"github.com/rroblf01/postman-rabbit/internal/storage"
)

type Backend struct {
	auth     *auth.Manager
	storage  *storage.Manager
	delivery *delivery.Outbound
	dkim     *dkim.Signer
	hostname string
	domain   string
}

func NewBackend(authMgr *auth.Manager, store *storage.Manager, del *delivery.Outbound, dkimSigner *dkim.Signer, hostname, domain string) *Backend {
	return &Backend{
		auth:     authMgr,
		storage:  store,
		delivery: del,
		dkim:     dkimSigner,
		hostname: hostname,
		domain:   domain,
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
			return fmt.Errorf("relay not allowed for unauthenticated session")
		}
		signed, err := s.backend.dkim.Sign(data, s.from, fmt.Sprintf("<%d.%d@%s>", 0, 0, s.backend.hostname))
		if err != nil {
			log.Printf("DKIM sign: %v", err)
			signed = data
		}
		if err := s.backend.delivery.Deliver(s.from, remoteRcpts, signed); err != nil {
			log.Printf("delivery error: %v", err)
			return err
		}
	}

	return nil
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
	for _, user := range users {
		us, err := s.backend.storage.ForUser(user)
		if err != nil {
			log.Printf("storage for %s: %v", user, err)
			continue
		}
		if _, err := us.Deliver(bytes.NewReader(data)); err != nil {
			log.Printf("deliver to %s: %v", user, err)
			continue
		}
		log.Printf("delivered to %s", user)
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
