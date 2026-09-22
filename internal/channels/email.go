package channels

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/hussein/ai-salesperson/internal/domain"
	"github.com/hussein/ai-salesperson/internal/netguard"
)

// Email sends through the customer's own SMTP server.
type Email struct {
	cfg          domain.EmailSettings
	allowPrivate bool
}

func NewEmail(cfg domain.EmailSettings, allowPrivate bool) *Email {
	return &Email{cfg: cfg, allowPrivate: allowPrivate}
}

func (e *Email) Name() string { return "email" }
func (e *Email) Recipient(email, _ string) string {
	return strings.TrimSpace(email)
}

func (e *Email) Send(ctx context.Context, o Outgoing) error {
	to, err := mail.ParseAddress(o.To)
	if err != nil {
		return fmt.Errorf("invalid recipient address")
	}
	from, err := mail.ParseAddress(e.cfg.FromAddress)
	if err != nil {
		return fmt.Errorf("invalid sender address in settings")
	}
	msg := buildMessage(e.cfg.FromName, from.Address, to.Address, o.Subject, o.Body)
	return e.deliver(ctx, from.Address, to.Address, msg)
}

// clean removes CR/LF so user- or model-supplied text can never inject headers.
func clean(s string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(s)
}

func buildMessage(fromName, from, to, subject, body string) []byte {
	var b bytes.Buffer
	dom := "localhost"
	if i := strings.LastIndex(from, "@"); i >= 0 {
		dom = from[i+1:]
	}
	id := make([]byte, 12)
	_, _ = rand.Read(id)
	fromHdr := (&mail.Address{Name: clean(fromName), Address: from}).String()
	h := func(k, v string) { b.WriteString(k + ": " + v + "\r\n") }
	h("From", fromHdr)
	h("To", to)
	h("Subject", mime.QEncoding.Encode("utf-8", clean(subject)))
	h("Date", time.Now().Format(time.RFC1123Z))
	h("Message-ID", "<"+hex.EncodeToString(id)+"@"+dom+">")
	h("List-Unsubscribe", "<mailto:"+from+"?subject=unsubscribe>")
	h("MIME-Version", "1.0")
	h("Content-Type", `text/plain; charset="UTF-8"`)
	h("Content-Transfer-Encoding", "quoted-printable")
	b.WriteString("\r\n")
	qw := quotedprintable.NewWriter(&b)
	_, _ = qw.Write([]byte(body))
	_ = qw.Close()
	return b.Bytes()
}

func (e *Email) dial(ctx context.Context) (*smtp.Client, error) {
	s := e.cfg.SMTP
	port := s.Port
	if port == 0 {
		port = map[string]int{"tls": 465, "none": 25}[s.Security]
		if port == 0 {
			port = 587
		}
	}
	addr := net.JoinHostPort(s.Host, strconv.Itoa(port))
	d := netguard.Dialer(e.allowPrivate)
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("connect to SMTP server: %w", err)
	}
	deadline := time.Now().Add(60 * time.Second)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	_ = conn.SetDeadline(deadline)
	tcfg := &tls.Config{ServerName: s.Host, MinVersion: tls.VersionTLS12}
	if s.Security == "tls" {
		conn = tls.Client(conn, tcfg)
	}
	c, err := smtp.NewClient(conn, s.Host)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("SMTP greeting: %w", err)
	}
	if s.Security == "starttls" || s.Security == "" {
		if err := c.StartTLS(tcfg); err != nil {
			c.Close()
			return nil, fmt.Errorf("SMTP STARTTLS: %w", err)
		}
	}
	if s.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", s.Username, s.Password, s.Host)); err != nil {
			c.Close()
			return nil, fmt.Errorf("SMTP login failed: %w", err)
		}
	}
	return c, nil
}

func (e *Email) deliver(ctx context.Context, from, to string, msg []byte) error {
	c, err := e.dial(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.Mail(from); err != nil {
		return fmt.Errorf("SMTP sender rejected: %w", err)
	}
	if err := c.Rcpt(to); err != nil {
		return fmt.Errorf("SMTP recipient rejected: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("SMTP delivery failed: %w", err)
	}
	return c.Quit()
}

// CheckSMTP verifies host, TLS and credentials without sending anything.
func CheckSMTP(ctx context.Context, cfg domain.EmailSettings, allowPrivate bool) error {
	c, err := NewEmail(cfg, allowPrivate).dial(ctx)
	if err != nil {
		return err
	}
	return c.Quit()
}
