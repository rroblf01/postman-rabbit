package config

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Hostname    string
	Domain      string
	MailDir     string
	TLSCertFile string
	TLSKeyFile  string

	SMTPPort       int
	SubmissionPort int
	SMTPSPort      int
	IMAPPort       int
	IMAPSPort      int

	Users map[string]string

	DKIMSelector string
	DKIMKeyFile  string

	RelayEnabled bool
}

func FromEnv() (*Config, error) {
	cfg := &Config{
		Hostname:       getEnv("HOSTNAME", "mail.localhost"),
		Domain:         getEnv("DOMAIN", "localhost"),
		MailDir:        getEnv("MAILDIR", "/var/mail"),
		TLSCertFile:    getEnv("TLS_CERT", ""),
		TLSKeyFile:     getEnv("TLS_KEY", ""),
		SMTPPort:       getEnvInt("SMTP_PORT", 25),
		SubmissionPort: getEnvInt("SUBMISSION_PORT", 587),
		SMTPSPort:      getEnvInt("SMTPS_PORT", 465),
		IMAPPort:       getEnvInt("IMAP_PORT", 143),
		IMAPSPort:      getEnvInt("IMAPS_PORT", 993),
		DKIMSelector:   getEnv("DKIM_SELECTOR", "default"),
		DKIMKeyFile:    getEnv("DKIM_KEY_FILE", ""),
		RelayEnabled:   getEnvBool("RELAY_ENABLED", true),
		Users:          make(map[string]string),
	}

	usersRaw := getEnv("USERS", "")
	if usersRaw == "" {
		return nil, fmt.Errorf("USERS environment variable is required (format: user1:pass1,user2:pass2)")
	}
	for _, pair := range strings.Split(usersRaw, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		parts := strings.SplitN(pair, ":", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return nil, fmt.Errorf("invalid user entry: %q (expected user:pass)", pair)
		}
		cfg.Users[parts[0]] = parts[1]
	}

	if cfg.TLSCertFile != "" && cfg.TLSKeyFile == "" {
		return nil, fmt.Errorf("TLS_CERT set but TLS_KEY is empty")
	}
	if cfg.TLSKeyFile != "" && cfg.TLSCertFile == "" {
		return nil, fmt.Errorf("TLS_KEY set but TLS_CERT is empty")
	}

	if cfg.DKIMKeyFile != "" && cfg.DKIMSelector == "" {
		return nil, fmt.Errorf("DKIM_KEY_FILE set but DKIM_SELECTOR is empty")
	}

	return cfg, nil
}

func (c *Config) ListenAddrs() []string {
	var addrs []string
	if c.SMTPPort > 0 {
		addrs = append(addrs, net.JoinHostPort("0.0.0.0", strconv.Itoa(c.SMTPPort)))
	}
	if c.SubmissionPort > 0 {
		addrs = append(addrs, net.JoinHostPort("0.0.0.0", strconv.Itoa(c.SubmissionPort)))
	}
	if c.SMTPSPort > 0 {
		addrs = append(addrs, net.JoinHostPort("0.0.0.0", strconv.Itoa(c.SMTPSPort)))
	}
	if c.IMAPPort > 0 {
		addrs = append(addrs, net.JoinHostPort("0.0.0.0", strconv.Itoa(c.IMAPPort)))
	}
	if c.IMAPSPort > 0 {
		addrs = append(addrs, net.JoinHostPort("0.0.0.0", strconv.Itoa(c.IMAPSPort)))
	}
	return addrs
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func getEnvBool(key string, fallback bool) bool {
	if v := os.Getenv(key); v != "" {
		switch strings.ToLower(v) {
		case "true", "1", "yes":
			return true
		case "false", "0", "no":
			return false
		}
	}
	return fallback
}
