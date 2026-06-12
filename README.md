# Postman Rabbit

A self-hosted, all-in-one mail server written in Go, distributed as a single
tiny (~6 MB) Docker image built `FROM scratch`. No external dependencies, no
database — mail is stored on disk in standard Maildir format.

It speaks SMTP (to send and receive) and IMAP (to read), so a normal mail client
like Thunderbird, Apple Mail or the Gmail app can use it as a full mailbox.

## Features

- **SMTP** (port 25): receive email from other servers (MX)
- **SMTP Submission** (port 587, STARTTLS): authenticated clients send email
- **SMTPS** (port 465, implicit TLS): TLS-from-the-start submission alternative
- **IMAP** (port 143, STARTTLS): read stored email from any client
- **IMAPS** (port 993, implicit TLS): TLS-from-the-start IMAP alternative
- **Maildir storage**: one file per message, with monotonic per-mailbox UIDs
- **DKIM signing**: outgoing mail is signed (RFC 6376) for deliverability
- **Outbound delivery**: relays authenticated mail to remote MX servers, with
  opportunistic STARTTLS
- **User authentication**: PLAIN and LOGIN SASL mechanisms, constant-time
  password comparison
- **Graceful shutdown**: in-flight sessions finish on SIGTERM/SIGINT

## Quick Start

```bash
docker run -d \
  --name postman-rabbit \
  -p 25:25 -p 587:587 -p 465:465 -p 143:143 -p 993:993 \
  -v maildata:/var/mail \
  -e HOSTNAME=mail.example.com \
  -e DOMAIN=example.com \
  -e USERS="alice:a-strong-password,bob:another-password" \
  ghcr.io/rroblf01/postman-rabbit:latest
```

Without a TLS certificate, the implicit-TLS ports (465, 993) stay disabled and
STARTTLS is unavailable — fine for a first test, but configure TLS before using
it for real (see [TLS](#tls-certificates)).

> **Note on running as root:** the image is `FROM scratch` (no users), so the
> process runs as root inside the container. That is what lets it bind the
> privileged mail ports (<1024) without extra capabilities. The container is
> otherwise empty — no shell, no package manager — and runs unprivileged
> relative to the host. If you prefer, remap ports above 1024 and put a
> reverse-proxy/firewall in front.

## Publishing the Docker image to GitHub

This repo ships a GitHub Actions workflow
([.github/workflows/docker-publish.yml](.github/workflows/docker-publish.yml))
that builds the multi-arch (amd64 + arm64) image and pushes it to the GitHub
Container Registry (**ghcr.io**) automatically:

- every push to the default branch → `:latest` and `:sha-<commit>`
- every version tag `vX.Y.Z` → `:X.Y.Z`, `:X.Y` and `:latest`

It authenticates with the built-in `GITHUB_TOKEN`, so no secrets to configure.
After the first successful run:

1. Open your repo → **Packages** → `postman-rabbit`.
2. Set the package visibility to **Public** (Package settings) if you want to
   pull it without logging in.
3. Pull and run it on your VPS:

```bash
docker pull ghcr.io/rroblf01/postman-rabbit:latest
```

To cut a versioned release:

```bash
git tag v1.0.0
git push origin v1.0.0   # triggers the workflow, publishes :1.0.0
```

## Configuration via Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `HOSTNAME` | `mail.localhost` | Server hostname; must match its forward (A) and reverse (PTR) DNS |
| `DOMAIN` | `localhost` | Mail domain this server is authoritative for |
| `USERS` | _(required)_ | Comma-separated `user:password` pairs |
| `MAILDIR` | `/var/mail` | Path to mail storage directory |
| `TLS_CERT` | _(empty)_ | Path to TLS certificate (PEM, fullchain) |
| `TLS_KEY` | _(empty)_ | Path to TLS private key (PEM) |
| `SMTP_PORT` | `25` | Inbound SMTP port (set `0` to disable) |
| `SUBMISSION_PORT` | `587` | Submission port (set `0` to disable) |
| `SMTPS_PORT` | `465` | SMTPS port — needs TLS, else skipped |
| `IMAP_PORT` | `143` | IMAP port (set `0` to disable) |
| `IMAPS_PORT` | `993` | IMAPS port — needs TLS, else skipped |
| `DKIM_SELECTOR` | `default` | DKIM DNS selector |
| `DKIM_KEY_FILE` | _(empty)_ | Path to DKIM private key (PEM); empty = no signing |
| `RELAY_ENABLED` | `true` | Allow authenticated users to relay to remote domains |

## Docker Compose

See [docker-compose.yml](docker-compose.yml). Edit `HOSTNAME`, `DOMAIN`,
`USERS`, then:

```bash
docker compose up -d
```

## DNS Requirements

Email delivery depends almost entirely on correct DNS. **Assume your domain is
`example.com` and your VPS public IP is `203.0.113.10`.** Create these records:

| Type | Name (host) | Value | Purpose |
|------|-------------|-------|---------|
| `A` | `mail` | `203.0.113.10` | `mail.example.com` resolves to your server |
| `MX` | `@` | `10 mail.example.com.` | tells the world where to deliver `@example.com` mail |
| `TXT` | `@` | `v=spf1 mx ~all` | SPF: only your MX hosts may send for the domain |
| `TXT` | `default._domainkey` | `v=DKIM1; k=rsa; p=MIID...` | DKIM public key (see below) |
| `TXT` | `_dmarc` | `v=DMARC1; p=quarantine; rua=mailto:postmaster@example.com` | DMARC policy + reports |

In typical DNS UIs that means, for `example.com`:

```
mail                  IN  A     203.0.113.10
@                     IN  MX 10  mail.example.com.
@                     IN  TXT    "v=spf1 mx ~all"
default._domainkey    IN  TXT    "v=DKIM1; k=rsa; p=MIIBIjANBgkqhkiG9w0..."
_dmarc                IN  TXT    "v=DMARC1; p=quarantine; rua=mailto:postmaster@example.com"
```

### Reverse DNS (PTR) — do not skip this

Most big providers (Gmail, Outlook) reject or spam-folder mail from IPs whose
**PTR record** doesn't match the sending hostname. PTR is set at your **VPS/IP
provider**, not your DNS host. Set:

```
203.0.113.10  ->  mail.example.com
```

Verify forward and reverse match:

```bash
dig +short mail.example.com      # must return 203.0.113.10
dig +short -x 203.0.113.10       # must return mail.example.com.
```

## TLS Certificates

Use a real certificate so clients can connect securely and STARTTLS works.
With [Let's Encrypt](https://letsencrypt.org/) (certbot) on the host:

```bash
certbot certonly --standalone -d mail.example.com
```

This produces `/etc/letsencrypt/live/mail.example.com/{fullchain,privkey}.pem`.
Mount them into the container and point `TLS_CERT` / `TLS_KEY` at them:

```bash
docker run -d --name postman-rabbit \
  -p 25:25 -p 587:587 -p 465:465 -p 143:143 -p 993:993 \
  -v maildata:/var/mail \
  -v /etc/letsencrypt/live/mail.example.com:/etc/mailserver/certs:ro \
  -e HOSTNAME=mail.example.com \
  -e DOMAIN=example.com \
  -e USERS="alice:a-strong-password" \
  -e TLS_CERT=/etc/mailserver/certs/fullchain.pem \
  -e TLS_KEY=/etc/mailserver/certs/privkey.pem \
  ghcr.io/rroblf01/postman-rabbit:latest
```

Restart the container after each certificate renewal so it reloads the cert.

## Generating a DKIM Key

DKIM proves the mail really came from your domain and wasn't altered. Generate
a key pair (here with the `default` selector for `example.com`):

```bash
docker run --rm -v "$(pwd)":/out golang:alpine sh -c "
  go install github.com/emersion/go-msgauth/cmd/dkim-keygen@latest && \
  cd /out && dkim-keygen -d example.com -s default
"
```

This writes the private key and prints the DNS TXT record to publish:

1. Move the generated private key to where you mount DKIM keys, named after the
   selector — e.g. `./dkim/default.pem` (mounted at
   `/etc/mailserver/dkim/default.pem`).
2. Publish the printed `default._domainkey TXT "v=DKIM1; k=rsa; p=..."` record
   in DNS (see the DNS table above).
3. Set `DKIM_SELECTOR=default` and
   `DKIM_KEY_FILE=/etc/mailserver/dkim/default.pem`.

Verify it once mail is flowing by sending to a Gmail address and checking
*Show original* → DKIM: PASS, or use https://www.mail-tester.com/.

## Usage with Email Clients

Configure your client (Thunderbird, Apple Mail, Outlook, ...) with:

| Setting | Value |
|---------|-------|
| **Email address** | `alice@example.com` |
| **Username** | `alice` (the part before `:` in `USERS`) |
| **Password** | the matching password from `USERS` |
| **IMAP server** | `mail.example.com`, port `993`, SSL/TLS |
| **SMTP server** | `mail.example.com`, port `587`, STARTTLS |

(Port `465` implicit-TLS for SMTP and port `143` STARTTLS for IMAP also work.)

## Architecture

```
internal/config    environment parsing & validation
internal/auth      user store, constant-time password check
internal/smtp      SMTP backend: AUTH, local delivery, relay, DKIM signing
internal/delivery  outbound delivery to remote MX (MX lookup + STARTTLS)
internal/dkim      DKIM signing via go-msgauth
internal/storage   Maildir-backed per-user storage
internal/imap      IMAP backend over Maildir, with a persistent UID list
cmd/mailserver     wires it together, starts listeners, graceful shutdown
```

## Security Notes

- **Plaintext auth is allowed** so submission works before/without TLS
  (`AllowInsecureAuth`). Configure TLS and have clients use 587/STARTTLS or
  465/implicit TLS so credentials are never sent in the clear.
- **Outbound STARTTLS is opportunistic** (used when the remote MX offers it,
  without certificate verification) — standard for MTA-to-MTA, protects against
  passive eavesdropping but not active MITM.
- **Relay is restricted**: only authenticated sessions may send to remote
  domains, and only when `RELAY_ENABLED=true`. This is *not* an open relay.
- Passwords are supplied in plaintext via `USERS`; treat that env var as a
  secret (use Docker/compose secrets or an `.env` file, not your shell history).

## Limitations

These are known trade-offs of a deliberately small server:

- **No outbound retry queue.** If a remote MX is temporarily unreachable,
  delivery returns a temporary error to the sending client rather than queuing
  and retrying later. Local delivery failures also return a temporary error
  (so the sender retries) instead of silently dropping mail.
- **IMAP SEARCH** supports a practical subset (flags, UID/seq sets, size, date,
  and header/body/text substring); unknown criteria are treated as matches.
- **No spam filtering / RBL / greylisting.** Pair with a downstream filter if
  you need it.
- **Single node.** Maildir on a local volume; no clustering or shared storage.

## Troubleshooting

- **Outbound mail to nobody / connection timeouts on port 25:** many ISPs and
  cloud providers block outbound TCP/25. Check with your VPS provider and
  request an unblock, or use a smarthost. Inbound 25 also needs to be open.
- **Gmail/Outlook reject or spam-folder your mail:** almost always missing PTR,
  SPF, DKIM or DMARC. Re-check the DNS section and test at
  https://www.mail-tester.com/.
- **Ports 465/993 not listening:** you didn't set `TLS_CERT`/`TLS_KEY`; the log
  prints a warning and those ports stay disabled by design.
- **Client can't authenticate:** username is the part before `:` in `USERS`
  (e.g. `alice`), not the full email address.

## Building from Source

```bash
git clone https://github.com/rroblf01/postman-rabbit.git
cd postman-rabbit
go build -o mailserver ./cmd/mailserver
go test ./...
```

## License

MIT
