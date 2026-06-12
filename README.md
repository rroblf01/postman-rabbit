# Postman Rabbit

A self-hosted, all-in-one mail server written in Go, distributed as a single Docker image. No external dependencies.

## Features

- **SMTP** (port 25): Receive email from other servers (MX)
- **SMTP Submission** (port 587 with STARTTLS): Send email from clients
- **SMTPS** (port 465 with implicit TLS): Secure SMTP alternative
- **IMAP** (port 143 with STARTTLS): Read stored email from any client
- **IMAPS** (port 993 with implicit TLS): Secure IMAP alternative
- **Maildir storage**: Standard mail format, one file per message
- **DKIM signing**: Sign outgoing email for better deliverability
- **Outbound delivery**: Relay email to external MX servers
- **User authentication**: PLAIN and LOGIN SASL mechanisms

## Quick Start

```bash
docker run -d \
  --name postman-rabbit \
  -p 25:25 -p 587:587 -p 143:143 \
  -v maildata:/var/mail \
  -e HOSTNAME=mail.yourdomain.com \
  -e DOMAIN=yourdomain.com \
  -e USERS="alice:password123,bob:secret456" \
  ghcr.io/rroblf01/postman-rabbit:latest
```

## Configuration via Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `HOSTNAME` | `mail.localhost` | Server hostname (must match reverse DNS) |
| `DOMAIN` | `localhost` | Domain this server handles |
| `USERS` | _(required)_ | Comma-separated list of `user:password` pairs |
| `MAILDIR` | `/var/mail` | Path to mail storage directory |
| `TLS_CERT` | _(empty)_ | Path to TLS certificate file |
| `TLS_KEY` | _(empty)_ | Path to TLS private key file |
| `SMTP_PORT` | `25` | SMTP incoming port |
| `SUBMISSION_PORT` | `587` | SMTP submission port |
| `SMTPS_PORT` | `465` | SMTPS port (requires TLS) |
| `IMAP_PORT` | `143` | IMAP port |
| `IMAPS_PORT` | `993` | IMAPS port (requires TLS) |
| `DKIM_SELECTOR` | `default` | DKIM DNS selector |
| `DKIM_KEY_FILE` | _(empty)_ | Path to DKIM private key (PEM) |
| `RELAY_ENABLED` | `true` | Allow relaying for authenticated users |

## Docker Compose

```yaml
services:
  mailserver:
    image: ghcr.io/rroblf01/postman-rabbit:latest
    container_name: postman-rabbit
    hostname: mail.yourdomain.com
    restart: unless-stopped
    ports:
      - "25:25"
      - "587:587"
      - "465:465"
      - "143:143"
      - "993:993"
    volumes:
      - maildata:/var/mail
      - ./certs:/etc/mailserver/certs:ro
    environment:
      HOSTNAME: mail.yourdomain.com
      DOMAIN: yourdomain.com
      USERS: "alice:password123"
      TLS_CERT: /etc/mailserver/certs/fullchain.pem
      TLS_KEY: /etc/mailserver/certs/privkey.pem
```

## Usage with Email Clients

Configure your email client (Gmail, Outlook, Thunderbird) with:

| Setting | Value |
|---------|-------|
| **Email address** | `youruser@yourdomain.com` |
| **Username** | `youruser` |
| **Password** | your password from `USERS` |
| **IMAP server** | `mail.yourdomain.com` |
| **IMAP port** | `993` |
| **IMAP security** | SSL/TLS |
| **SMTP server** | `mail.yourdomain.com` |
| **SMTP port** | `587` |
| **SMTP security** | STARTTLS |

## DNS Requirements

For proper email delivery you **must** configure these DNS records:

| Record | Value |
|--------|-------|
| `A` for `mail` | Your VPS IP address |
| `MX` `@` | `mail.yourdomain.com.` |
| `TXT` `@` | `v=spf1 mx ~all` |
| `TXT` `default._domainkey` | Your DKIM public key |
| `TXT` `_dmarc` | `v=DMARC1; p=quarantine;` |

A reverse PTR record pointing `mail.yourdomain.com` to your VPS IP is strongly recommended.

## Generating a DKIM Key

```bash
docker run --rm -v $(pwd):/out golang:alpine sh -c "
  go install github.com/emersion/go-msgauth/cmd/dkim-keygen@latest
  dkim-keygen -d yourdomain.com -s default > /out/dkim.txt
  mv dkim-private.pem /out/
"
```

This creates a `dkim-private.pem` file (mount to `/etc/mailserver/dkim/default.pem`) and prints the DNS TXT record to add to your domain.

## Building from Source

```bash
git clone https://github.com/rroblf01/postman-rabbit.git
cd postman-rabbit
go build -o mailserver ./cmd/mailserver
```

## License

MIT
