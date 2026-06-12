package dkim

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

type Signer struct {
	selector string
	domain   string
	key      *rsa.PrivateKey
}

func NewSigner(selector, domain, keyFile string) (*Signer, error) {
	if keyFile == "" {
		return nil, nil
	}

	data, err := os.ReadFile(keyFile)
	if err != nil {
		return nil, fmt.Errorf("read DKIM key: %w", err)
	}

	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("no PEM block in DKIM key file")
	}

	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		key, err = x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse DKIM private key: %w", err)
		}
	}

	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("DKIM key is not RSA")
	}

	return &Signer{
		selector: selector,
		domain:   domain,
		key:      rsaKey,
	}, nil
}

func (s *Signer) Sign(body []byte, from string, msgID string, extra ...string) ([]byte, error) {
	subject := ""
	if len(extra) > 0 {
		subject = extra[0]
	}
	if s == nil {
		return body, nil
	}

	headers := fmt.Sprintf("from:%s\r\nsubject:%s\r\nmessage-id:%s\r\ndate:%s\r\n",
		from, subject, msgID, time.Now().Format(time.RFC1123Z))

	canonBody := strings.ReplaceAll(string(bytes.ReplaceAll(body, []byte("\r\n"), []byte("\n"))), "\n", "\r\n")
	if !strings.HasSuffix(canonBody, "\r\n") {
		canonBody += "\r\n"
	}

	bodyHash := sha256.Sum256([]byte(canonBody))

	sigHeader := fmt.Sprintf("v=1; a=rsa-sha256; c=relaxed/relaxed; d=%s; s=%s; t=%d; bh=%s; h=from:subject:message-id:date;",
		s.domain, s.selector, time.Now().Unix(), base64.StdEncoding.EncodeToString(bodyHash[:]))

	sigData := []byte(fmt.Sprintf("dkim-signature:%s", sigHeader))
	headerHash := sha256.Sum256(sigData)

	signature, err := rsa.SignPKCS1v15(rand.Reader, s.key, crypto.SHA256, headerHash[:])
	if err != nil {
		return nil, fmt.Errorf("sign DKIM: %w", err)
	}

	b := &bytes.Buffer{}
	fmt.Fprintf(b, "DKIM-Signature: %s b=%s;\r\n", sigHeader, base64.StdEncoding.EncodeToString(signature))
	b.WriteString(headers)
	io.Copy(b, bytes.NewReader(body))

	return b.Bytes(), nil
}
