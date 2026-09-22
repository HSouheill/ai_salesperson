// Package channels holds outbound communication adapters. Every customer
// connects their own accounts (SMTP mailbox, WhatsApp Business number); the
// platform never sends from a shared identity. The sales service only calls
// Send after a message passed approval and compliance checks.
package channels

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"

	"github.com/hussein/ai-salesperson/internal/domain"
)

// ErrNotConfigured means the organization has not connected this channel yet.
var ErrNotConfigured = errors.New("channel not configured")

type Outgoing struct {
	To      string // email address or phone number
	Subject string
	Body    string
	// InWindow: the prospect wrote to us within the last 24h. WhatsApp only
	// allows free-text replies inside that window.
	InWindow bool
}

type Channel interface {
	Name() string
	// Recipient returns the address to use from the prospect's contact
	// details, or "" if the prospect cannot be reached on this channel.
	Recipient(email, phone string) string
	Send(ctx context.Context, o Outgoing) error
}

// Provider builds a Channel from an organization's own settings.
type Provider interface {
	For(org domain.Org, name string) (Channel, error)
}

var Known = []string{"email", "whatsapp"}

func IsKnown(name string) bool {
	for _, k := range Known {
		if k == name {
			return true
		}
	}
	return false
}

// Tenant is the production Provider.
type Tenant struct {
	// AllowPrivate lets tenants point at private-network servers (self-hosted only).
	AllowPrivate bool
	// DevLog delivers by logging when a tenant has not configured a channel.
	// Development only: it makes "sent" mean nothing.
	DevLog bool
	HTTP   *http.Client
	// GraphBase overrides the WhatsApp API host (tests).
	GraphBase string
}

func (t Tenant) For(org domain.Org, name string) (Channel, error) {
	switch name {
	case "email":
		if e := org.Settings.Email; e != nil && e.SMTP.Host != "" && e.FromAddress != "" {
			return NewEmail(*e, t.AllowPrivate), nil
		}
	case "whatsapp":
		if w := org.Settings.WhatsApp; w != nil && w.PhoneNumberID != "" && w.AccessToken != "" {
			return NewWhatsApp(*w, t.HTTP, t.GraphBase, t.AllowPrivate), nil
		}
	default:
		return nil, fmt.Errorf("unknown channel %q", name)
	}
	if t.DevLog {
		return Log{name: name, phone: name == "whatsapp"}, nil
	}
	return nil, fmt.Errorf("%w: connect %s in Settings", ErrNotConfigured, name)
}

// Log is a development channel that only logs.
type Log struct {
	name  string
	phone bool
}

func (l Log) Name() string { return l.name }
func (l Log) Recipient(email, phone string) string {
	if l.phone {
		return phone
	}
	return email
}
func (l Log) Send(_ context.Context, o Outgoing) error {
	log.Printf("[channel:%s DEV-LOG, not delivered] to=%s subject=%q body=%q", l.name, o.To, o.Subject, o.Body)
	return nil
}
