FROM golang:1.23-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /usr/local/bin/mailserver ./cmd/mailserver

FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata

RUN addgroup -S mail && adduser -S mail -G mail

COPY --from=builder /usr/local/bin/mailserver /usr/local/bin/mailserver

RUN mkdir -p /var/mail /etc/mailserver && chown -R mail:mail /var/mail

USER mail

EXPOSE 25 587 465 143 993

VOLUME ["/var/mail", "/etc/mailserver"]

ENV HOSTNAME=mail.localhost \
    DOMAIN=localhost \
    MAILDIR=/var/mail \
    SMTP_PORT=25 \
    SUBMISSION_PORT=587 \
    SMTPS_PORT=465 \
    IMAP_PORT=143 \
    IMAPS_PORT=993 \
    TLS_CERT="" \
    TLS_KEY="" \
    USERS="" \
    DKIM_SELECTOR=default \
    DKIM_KEY_FILE="" \
    RELAY_ENABLED=true

ENTRYPOINT ["/usr/local/bin/mailserver"]
