// Package integrations pushes qualified leads to the customer's own CRM.
// The generic signed webhook works with anything (Zapier, Make, custom CRMs);
// HubSpot is supported natively with the customer's private-app token.
package integrations

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/hussein/ai-salesperson/internal/domain"
	"github.com/hussein/ai-salesperson/internal/netguard"
)

type Event string

const (
	LeadQualified Event = "lead.qualified"
	MeetingBooked Event = "meeting.booked"
	DealWon       Event = "deal.won"
)

// Pusher is what the sales service depends on.
type Pusher interface {
	// Push sends the event to every integration the org configured and returns
	// the external CRM id, if one was created.
	Push(ctx context.Context, org domain.Org, agent domain.Agent, p domain.Prospect, ev Event) (crmID string, err error)
}

type Client struct {
	AllowPrivate bool
	HubSpotBase  string // default https://api.hubapi.com
	http         *http.Client
}

func New(allowPrivate bool) *Client {
	return &Client{AllowPrivate: allowPrivate, http: &http.Client{
		Timeout:   15 * time.Second,
		Transport: &http.Transport{DialContext: netguard.Dialer(allowPrivate).DialContext},
		// Never follow redirects with a signed payload or bearer token.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func (c *Client) Push(ctx context.Context, org domain.Org, agent domain.Agent, p domain.Prospect, ev Event) (string, error) {
	in := org.Settings.Integrations
	var errs []error
	crmID := p.CRMID
	if in.WebhookURL != "" {
		if err := c.webhook(ctx, in, org, agent, p, ev); err != nil {
			errs = append(errs, fmt.Errorf("webhook: %w", err))
		}
	}
	if in.HubSpotToken != "" {
		id, err := c.hubspot(ctx, in.HubSpotToken, p, ev)
		if err != nil {
			errs = append(errs, fmt.Errorf("hubspot: %w", err))
		} else if id != "" {
			crmID = id
		}
	}
	return crmID, errors.Join(errs...)
}

func (c *Client) webhook(ctx context.Context, in domain.Integrations, org domain.Org, a domain.Agent, p domain.Prospect, ev Event) error {
	payload, _ := json.Marshal(map[string]any{
		"event": ev, "sent_at": time.Now().UTC(), "org_id": org.ID,
		"agent": map[string]string{"id": a.ID, "name": a.Name},
		"prospect": map[string]any{
			"id": p.ID, "name": p.Name, "contact_name": p.ContactName, "email": p.Contact.Email, "phone": p.Contact.Phone,
			"website": p.Contact.Website, "industry": p.Industry, "location": p.Location, "stage": p.Stage,
			"score": p.Score, "score_reasons": p.ScoreReasons, "deal_value": p.DealValue,
			"qualification_answers": p.QualificationAnswers, "meeting_at": p.MeetingAt,
		},
	})
	u, err := url.Parse(in.WebhookURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return errors.New("webhook URL must be http(s)")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-AISP-Event", string(ev))
	if in.WebhookSecret != "" {
		m := hmac.New(sha256.New, []byte(in.WebhookSecret))
		m.Write(payload)
		req.Header.Set("X-AISP-Signature", "sha256="+hex.EncodeToString(m.Sum(nil)))
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

var reExisting = regexp.MustCompile(`(?i)existing id:\s*(\d+)`)

func (c *Client) hubspot(ctx context.Context, token string, p domain.Prospect, ev Event) (string, error) {
	base := strings.TrimRight(c.HubSpotBase, "/")
	if base == "" {
		base = "https://api.hubapi.com"
	}
	call := func(method, path string, body any) (int, []byte, error) {
		b, _ := json.Marshal(body)
		req, err := http.NewRequestWithContext(ctx, method, base+path, bytes.NewReader(b))
		if err != nil {
			return 0, nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := c.http.Do(req)
		if err != nil {
			return 0, nil, err
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return resp.StatusCode, raw, nil
	}
	stage := "salesqualifiedlead"
	if ev == DealWon {
		stage = "customer"
	}
	// Already pushed earlier: just move the lifecycle stage.
	if p.CRMID != "" {
		code, raw, err := call(http.MethodPatch, "/crm/v3/objects/contacts/"+url.PathEscape(p.CRMID), map[string]any{"properties": map[string]string{"lifecyclestage": stage}})
		if err != nil {
			return "", err
		}
		if code/100 != 2 {
			return "", fmt.Errorf("update contact: status %d: %s", code, trunc(raw))
		}
		return p.CRMID, nil
	}
	if p.Contact.Email == "" {
		return "", errors.New("prospect has no email; HubSpot contacts need one")
	}
	first, last, _ := strings.Cut(strings.TrimSpace(p.ContactName), " ")
	props := map[string]string{"email": p.Contact.Email, "company": p.Name, "phone": p.Contact.Phone, "website": p.Contact.Website,
		"firstname": first, "lastname": last, "lifecyclestage": stage}
	for k, v := range props {
		if v == "" {
			delete(props, k)
		}
	}
	code, raw, err := call(http.MethodPost, "/crm/v3/objects/contacts", map[string]any{"properties": props})
	if err != nil {
		return "", err
	}
	switch {
	case code/100 == 2:
		var out struct{ ID string }
		_ = json.Unmarshal(raw, &out)
		return out.ID, nil
	case code == http.StatusConflict:
		if m := reExisting.FindSubmatch(raw); m != nil {
			return string(m[1]), nil
		}
	}
	return "", fmt.Errorf("create contact: status %d: %s", code, trunc(raw))
}

func trunc(b []byte) string {
	if len(b) > 200 {
		b = b[:200]
	}
	return string(b)
}
