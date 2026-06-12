package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-smtp"
	"github.com/rroblf01/postman-rabbit/internal/auth"
	"github.com/rroblf01/postman-rabbit/internal/config"
	"github.com/rroblf01/postman-rabbit/internal/delivery"
	"github.com/rroblf01/postman-rabbit/internal/dkim"
	imapbackend "github.com/rroblf01/postman-rabbit/internal/imap"
	smtpbackend "github.com/rroblf01/postman-rabbit/internal/smtp"
	"github.com/rroblf01/postman-rabbit/internal/storage"
)

// server is anything that can be gracefully shut down. Both go-smtp and
// go-imap servers satisfy it.
type server interface {
	Close() error
}

func main() {
	cfg, err := config.FromEnv()
	if err != nil {
		log.Fatalf("configuration error: %v", err)
	}

	store := storage.New(cfg.MailDir)
	authMgr := auth.New(cfg.Users)
	del := delivery.New(cfg.Hostname)

	dkimSigner, err := dkim.NewSigner(cfg.DKIMSelector, cfg.Domain, cfg.DKIMKeyFile)
	if err != nil {
		log.Printf("warning: DKIM not available (outgoing mail will be unsigned): %v", err)
	} else if dkimSigner == nil {
		log.Printf("notice: DKIM signing disabled (set DKIM_KEY_FILE to enable)")
	}

	var tlsConfig *tls.Config
	if cfg.TLSCertFile != "" && cfg.TLSKeyFile != "" {
		cert, err := tls.LoadX509KeyPair(cfg.TLSCertFile, cfg.TLSKeyFile)
		if err != nil {
			log.Fatalf("load TLS cert: %v", err)
		}
		tlsConfig = &tls.Config{
			Certificates: []tls.Certificate{cert},
			ServerName:   cfg.Hostname,
		}
	} else {
		log.Printf("notice: no TLS_CERT/TLS_KEY set; STARTTLS and the implicit-TLS ports (SMTPS/IMAPS) are disabled")
	}

	var (
		wg      sync.WaitGroup
		errCh   = make(chan error, 10)
		mu      sync.Mutex
		servers []server
	)

	track := func(s server) {
		mu.Lock()
		servers = append(servers, s)
		mu.Unlock()
	}

	// startListener opens a plaintext listener (optionally STARTTLS-capable) and
	// serves it in a goroutine, recording fatal errors on errCh.
	startListener := func(label, addr string, serveFn func(net.Listener) error) {
		lis, err := net.Listen("tcp", addr)
		if err != nil {
			errCh <- fmt.Errorf("listen %s (%s): %w", addr, label, err)
			return
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			log.Printf("listening on %s (%s)", addr, label)
			if err := serveFn(lis); err != nil {
				errCh <- fmt.Errorf("serve %s (%s): %w", addr, label, err)
			}
		}()
	}

	// startTLSListener opens an implicit-TLS listener (SMTPS/IMAPS).
	startTLSListener := func(label, addr string, serveFn func(net.Listener) error) {
		lis, err := tls.Listen("tcp", addr, tlsConfig)
		if err != nil {
			errCh <- fmt.Errorf("listen %s (%s): %w", addr, label, err)
			return
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			log.Printf("listening on %s (%s)", addr, label)
			if err := serveFn(lis); err != nil {
				errCh <- fmt.Errorf("serve %s (%s): %w", addr, label, err)
			}
		}()
	}

	newSMTPServer := func() *smtp.Server {
		s := smtp.NewServer(smtpbackend.NewBackend(authMgr, store, del, dkimSigner, cfg.Hostname, cfg.Domain, cfg.RelayEnabled))
		s.Domain = cfg.Hostname
		s.AllowInsecureAuth = true
		s.MaxMessageBytes = 25 * 1024 * 1024
		s.MaxRecipients = 50
		s.ReadTimeout = 5 * time.Minute
		s.WriteTimeout = 5 * time.Minute
		if tlsConfig != nil {
			s.TLSConfig = tlsConfig
		}
		track(s)
		return s
	}

	newIMAPServer := func() *imapserver.Server {
		srv := imapserver.New(&imapserver.Options{
			NewSession: func(c *imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
				return imapbackend.NewSession(authMgr, store), nil, nil
			},
			TLSConfig:    tlsConfig,
			InsecureAuth: true,
			Caps: imap.CapSet{
				imap.CapIMAP4rev2: {},
				imap.CapNamespace: {},
				imap.CapMove:      {},
				imap.CapUIDPlus:   {},
			},
		})
		track(srv)
		return srv
	}

	if cfg.SMTPPort > 0 {
		startListener("SMTP", fmt.Sprintf("0.0.0.0:%d", cfg.SMTPPort), newSMTPServer().Serve)
	}
	if cfg.SubmissionPort > 0 {
		startListener("Submission", fmt.Sprintf("0.0.0.0:%d", cfg.SubmissionPort), newSMTPServer().Serve)
	}
	if cfg.SMTPSPort > 0 {
		if tlsConfig == nil {
			log.Printf("warning: SMTPS_PORT=%d set but no TLS certificate; SMTPS disabled", cfg.SMTPSPort)
		} else {
			startTLSListener("SMTPS", fmt.Sprintf("0.0.0.0:%d", cfg.SMTPSPort), newSMTPServer().Serve)
		}
	}
	if cfg.IMAPPort > 0 {
		startListener("IMAP", fmt.Sprintf("0.0.0.0:%d", cfg.IMAPPort), newIMAPServer().Serve)
	}
	if cfg.IMAPSPort > 0 {
		if tlsConfig == nil {
			log.Printf("warning: IMAPS_PORT=%d set but no TLS certificate; IMAPS disabled", cfg.IMAPSPort)
		} else {
			startTLSListener("IMAPS", fmt.Sprintf("0.0.0.0:%d", cfg.IMAPSPort), newIMAPServer().Serve)
		}
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	log.Printf("Postman Rabbit mail server started on %s (domain %s)", cfg.Hostname, cfg.Domain)

	select {
	case sig := <-sigCh:
		log.Printf("received %v, shutting down gracefully", sig)
	case err := <-errCh:
		log.Printf("fatal server error: %v", err)
	}

	// Stop accepting new connections and let in-flight sessions finish.
	mu.Lock()
	for _, s := range servers {
		s.Close()
	}
	mu.Unlock()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	select {
	case <-done:
		log.Printf("shutdown complete")
	case <-ctx.Done():
		log.Printf("shutdown timed out, exiting")
	}
}
