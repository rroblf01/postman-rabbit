package dkim

import (
	"bytes"
	"crypto"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"

	"github.com/emersion/go-msgauth/dkim"
)

// Signer signs outgoing messages with a DKIM-Signature header using the
// configured selector, domain and RSA private key.
type Signer struct {
	selector string
	domain   string
	key      crypto.Signer
}

// NewSigner loads an RSA private key (PKCS#8 or PKCS#1, PEM-encoded) and returns
// a Signer. When keyFile is empty it returns (nil, nil): DKIM signing is then
// disabled and Sign becomes a no-op.
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

// Sign prepends a valid DKIM-Signature header to a complete RFC 5322 message
// (headers + body). The signature covers the message's actual headers and body
// using relaxed/relaxed canonicalization and rsa-sha256, so it verifies against
// the public key published in DNS. A nil Signer returns the message unchanged.
func (s *Signer) Sign(message []byte) ([]byte, error) {
	if s == nil {
		return message, nil
	}

	opts := &dkim.SignOptions{
		Domain:   s.domain,
		Selector: s.selector,
		Signer:   s.key,
		// HeaderKeys nil => sign exactly the headers present in the message
		// (From, To, Subject, Date, Message-ID, ...) with relaxed/relaxed
		// canonicalization and rsa-sha256.
	}

	var out bytes.Buffer
	if err := dkim.Sign(&out, bytes.NewReader(message), opts); err != nil {
		return nil, fmt.Errorf("sign DKIM: %w", err)
	}
	return out.Bytes(), nil
}
