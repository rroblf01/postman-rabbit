# ---- build stage ----
FROM golang:1.26-alpine AS builder

# ca-certificates so we can copy the CA bundle into the scratch image (needed
# for outbound STARTTLS certificate verification).
RUN apk add --no-cache ca-certificates

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Fully static binary (no libc), stripped, so it runs in scratch.
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /mailserver ./cmd/mailserver

# ---- runtime stage ----
FROM scratch

# CA bundle for verifying remote MX servers during outbound STARTTLS.
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=builder /mailserver /mailserver

# NOTE: scratch has no users, so the process runs as root (uid 0). This is what
# lets it bind the privileged mail ports (<1024) without extra capabilities.
# The container is otherwise empty — no shell, no package manager.

EXPOSE 25 587 465 143 993

VOLUME ["/var/mail", "/etc/mailserver"]

# Non-secret defaults only. USERS, TLS_CERT, TLS_KEY and DKIM_KEY_FILE are read
# from the environment at runtime (see README) and intentionally left unset here.
ENV HOSTNAME=mail.localhost \
    DOMAIN=localhost \
    MAILDIR=/var/mail \
    SMTP_PORT=25 \
    SUBMISSION_PORT=587 \
    SMTPS_PORT=465 \
    IMAP_PORT=143 \
    IMAPS_PORT=993 \
    DKIM_SELECTOR=default \
    RELAY_ENABLED=true

ENTRYPOINT ["/mailserver"]
