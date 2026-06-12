package main

import (
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"

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
		log.Printf("warning: DKIM not available: %v", err)
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
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 10)

	startListener := func(network, addr string, serveFn func(net.Listener) error) {
		lis, err := net.Listen(network, addr)
		if err != nil {
			errCh <- fmt.Errorf("listen %s: %w", addr, err)
			return
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			log.Printf("listening on %s", addr)
			if err := serveFn(lis); err != nil {
				errCh <- fmt.Errorf("serve %s: %w", addr, err)
			}
		}()
	}

	if cfg.SMTPPort > 0 {
		addr := fmt.Sprintf("0.0.0.0:%d", cfg.SMTPPort)
		s := smtp.NewServer(smtpbackend.NewBackend(authMgr, store, del, dkimSigner, cfg.Hostname, cfg.Domain))
		s.Domain = cfg.Hostname
		s.AllowInsecureAuth = true
		s.MaxMessageBytes = 25 * 1024 * 1024
		s.MaxRecipients = 50
		startListener("tcp", addr, s.Serve)
	}

	if cfg.SubmissionPort > 0 {
		addr := fmt.Sprintf("0.0.0.0:%d", cfg.SubmissionPort)
		s := smtp.NewServer(smtpbackend.NewBackend(authMgr, store, del, dkimSigner, cfg.Hostname, cfg.Domain))
		s.Domain = cfg.Hostname
		s.AllowInsecureAuth = true
		s.MaxMessageBytes = 25 * 1024 * 1024
		s.MaxRecipients = 50
		if tlsConfig != nil {
			s.TLSConfig = tlsConfig
		}
		startListener("tcp", addr, s.Serve)
	}

	if cfg.SMTPSPort > 0 && tlsConfig != nil {
		addr := fmt.Sprintf("0.0.0.0:%d", cfg.SMTPSPort)
		s := smtp.NewServer(smtpbackend.NewBackend(authMgr, store, del, dkimSigner, cfg.Hostname, cfg.Domain))
		s.Domain = cfg.Hostname
		s.AllowInsecureAuth = true
		s.MaxMessageBytes = 25 * 1024 * 1024
		s.MaxRecipients = 50
		lis, err := tls.Listen("tcp", addr, tlsConfig)
		if err != nil {
			log.Printf("cannot listen on %s: %v", addr, err)
		} else {
			wg.Add(1)
			go func() {
				defer wg.Done()
				log.Printf("listening on %s (SMTPS)", addr)
				if err := s.Serve(lis); err != nil {
					errCh <- fmt.Errorf("serve %s: %w", addr, err)
				}
			}()
		}
	}

	if cfg.IMAPPort > 0 {
		addr := fmt.Sprintf("0.0.0.0:%d", cfg.IMAPPort)
		imapSrv := imapserver.New(&imapserver.Options{
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
		startListener("tcp", addr, imapSrv.Serve)
	}

	if cfg.IMAPSPort > 0 && tlsConfig != nil {
		addr := fmt.Sprintf("0.0.0.0:%d", cfg.IMAPSPort)
		imapSrv := imapserver.New(&imapserver.Options{
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
		lis, err := tls.Listen("tcp", addr, tlsConfig)
		if err != nil {
			log.Printf("cannot listen on %s: %v", addr, err)
		} else {
			wg.Add(1)
			go func() {
				defer wg.Done()
				log.Printf("listening on %s (IMAPS)", addr)
				if err := imapSrv.Serve(lis); err != nil {
					errCh <- fmt.Errorf("serve %s: %w", addr, err)
				}
			}()
		}
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	log.Printf("Postman Rabbit mail server starting on %s", cfg.Hostname)

	select {
	case sig := <-sigCh:
		log.Printf("received %v, shutting down", sig)
	case err := <-errCh:
		log.Printf("server error: %v", err)
	}

	os.Exit(0)
}
