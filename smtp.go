package vigil

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strings"
)

// SMTPNotifier implements Notifier using Go's stdlib net/smtp.
// It is automatically used when Config.Notifier is nil and SMTP config is provided.
type SMTPNotifier struct {
	cfg        SMTPConfig
	recipients []string
}

// newSMTPNotifier creates an SMTP notifier from config.
func newSMTPNotifier(cfg SMTPConfig, recipients []string) *SMTPNotifier {
	return &SMTPNotifier{cfg: cfg, recipients: recipients}
}

func (n *SMTPNotifier) SendDigest(ctx context.Context, digest *Digest) error {
	return n.send(ctx, digestSubject(digest), renderDigest(digest))
}

func (n *SMTPNotifier) SendImmediate(ctx context.Context, event *Event) error {
	return n.send(ctx, immediateSubject(event), renderImmediate(event))
}

func (n *SMTPNotifier) send(ctx context.Context, subject, body string) error {
	addr := fmt.Sprintf("%s:%d", n.cfg.Host, n.cfg.Port)
	msg := buildMIMEMessage(n.cfg.From, n.recipients, subject, body)
	if n.cfg.UseTLS {
		return n.dialTLS(ctx, addr, msg)
	}
	return n.dialSTARTTLS(ctx, addr, msg)
}

// dialSTARTTLS connects plaintext then upgrades with STARTTLS (port 587 style).
func (n *SMTPNotifier) dialSTARTTLS(ctx context.Context, addr string, msg []byte) error {
	host, _, _ := net.SplitHostPort(addr)

	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("vigil smtp: dial %s: %w", addr, err)
	}

	c, err := smtp.NewClient(conn, host)
	if err != nil {
		return fmt.Errorf("vigil smtp: new client: %w", err)
	}
	defer c.Close()

	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(&tls.Config{ServerName: host}); err != nil {
			return fmt.Errorf("vigil smtp: starttls: %w", err)
		}
	}

	return n.authAndSend(c, host, msg)
}

// dialTLS connects directly over implicit TLS (port 465 style).
func (n *SMTPNotifier) dialTLS(ctx context.Context, addr string, msg []byte) error {
	host, _, _ := net.SplitHostPort(addr)

	rawConn, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("vigil smtp: dial %s: %w", addr, err)
	}
	tlsConn := tls.Client(rawConn, &tls.Config{ServerName: host})
	if err := tlsConn.Handshake(); err != nil {
		rawConn.Close()
		return fmt.Errorf("vigil smtp: tls handshake: %w", err)
	}

	c, err := smtp.NewClient(tlsConn, host)
	if err != nil {
		tlsConn.Close()
		return fmt.Errorf("vigil smtp: new client: %w", err)
	}
	defer c.Close()

	return n.authAndSend(c, host, msg)
}

func (n *SMTPNotifier) authAndSend(c *smtp.Client, host string, msg []byte) error {
	if n.cfg.Username != "" {
		auth := smtp.PlainAuth("", n.cfg.Username, n.cfg.Password, host)
		if err := c.Auth(auth); err != nil {
			return fmt.Errorf("vigil smtp: auth: %w", err)
		}
	}
	if err := c.Mail(n.cfg.From); err != nil {
		return fmt.Errorf("vigil smtp: MAIL FROM: %w", err)
	}
	for _, to := range n.recipients {
		if err := c.Rcpt(to); err != nil {
			return fmt.Errorf("vigil smtp: RCPT TO %s: %w", to, err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("vigil smtp: DATA: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("vigil smtp: write body: %w", err)
	}
	return w.Close()
}

func buildMIMEMessage(from string, to []string, subject, body string) []byte {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "From: %s\r\n", from)
	fmt.Fprintf(&buf, "To: %s\r\n", strings.Join(to, ", "))
	fmt.Fprintf(&buf, "Subject: %s\r\n", subject)
	buf.WriteString("MIME-Version: 1.0\r\n")
	buf.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	buf.WriteString("\r\n")
	buf.WriteString(body)
	return buf.Bytes()
}
