package sales

import (
	"context"
	"errors"
	"net/mail"
	"strings"

	"github.com/hussein/ai-salesperson/internal/store"
)

// HandleInbound routes a message that arrived on a channel (an email in the
// customer's mailbox, a WhatsApp webhook) to the prospect who sent it. Senders
// who are not prospects of this organization are ignored (matched = false):
// the customer's mailbox also holds unrelated mail.
func (s *Service) HandleInbound(ctx context.Context, orgID, channel, from, body string) (matched bool, err error) {
	var email, phone string
	if channel == "whatsapp" {
		phone = store.Digits(from)
	} else if addr, perr := mail.ParseAddress(strings.TrimSpace(from)); perr == nil {
		email = addr.Address
	}
	p, err := s.Store.FindProspectByContact(ctx, orgID, email, phone)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	_, err = s.handleReply(ctx, orgID, p.ID, channel, body)
	return true, err
}
