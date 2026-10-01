package sales

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/hussein/ai-salesperson/internal/domain"
)

// unsubscribeToken is a per-prospect token, verifiable without extra storage:
// it is an HMAC of the prospect ID keyed by the org's own InboundToken (a
// random secret already generated per org). It needs no database lookup to
// issue and no extra column to store.
func unsubscribeToken(orgSecret, prospectID string) string {
	m := hmac.New(sha256.New, []byte(orgSecret))
	m.Write([]byte(prospectID))
	return hex.EncodeToString(m.Sum(nil))
}

// UnsubscribeURL builds the one-click unsubscribe link for a prospect's email,
// or "" if the service has no public URL configured (e.g. in tests that don't
// need it) or the org has no inbound token yet.
func (s *Service) UnsubscribeURL(org domain.Org, prospectID string) string {
	if s.PublicURL == "" || org.InboundToken == "" {
		return ""
	}
	return fmt.Sprintf("%s/v1/unsubscribe/%s/%s/%s", strings.TrimRight(s.PublicURL, "/"), org.ID, prospectID, unsubscribeToken(org.InboundToken, prospectID))
}

// Unsubscribe verifies a one-click unsubscribe token and opts the prospect
// out. It is safe to expose without authentication: the token is unguessable
// and scoped to exactly one prospect.
func (s *Service) Unsubscribe(ctx context.Context, orgID, prospectID, token string) error {
	org, err := s.Store.GetOrg(ctx, orgID)
	if err != nil {
		return err
	}
	want := unsubscribeToken(org.InboundToken, prospectID)
	if len(token) != len(want) || !hmac.Equal([]byte(token), []byte(want)) {
		return fmt.Errorf("%w: invalid unsubscribe link", ErrInvalid)
	}
	p, err := s.Store.GetProspect(ctx, orgID, prospectID)
	if err != nil {
		return err
	}
	if p.Stage == domain.StageDoNotContact {
		return nil // already done; one-click unsubscribe can be clicked more than once
	}
	return s.optOut(ctx, &p)
}
