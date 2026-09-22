// Package billing connects plans to Stripe subscriptions (Checkout for
// purchases, the Billing Portal for management, webhooks for state).
package billing

import (
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
	"strconv"
	"strings"
	"time"

	"github.com/hussein/ai-salesperson/internal/domain"
)

type Stripe struct {
	Key           string
	WebhookSecret string
	Prices        map[string]string // plan -> Stripe price ID
	Base          string            // default https://api.stripe.com
	HTTP          *http.Client
}

var ErrNotConfigured = errors.New("billing is not configured on this server")

func (s *Stripe) Configured() bool { return s != nil && s.Key != "" }

func (s *Stripe) call(ctx context.Context, path string, form url.Values) (map[string]any, error) {
	base := s.Base
	if base == "" {
		base = "https://api.stripe.com"
	}
	hc := s.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+path, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+s.Key)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("stripe unreachable: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	if resp.StatusCode/100 != 2 {
		msg := fmt.Sprintf("status %d", resp.StatusCode)
		if e, ok := out["error"].(map[string]any); ok {
			if m, ok := e["message"].(string); ok {
				msg = m
			}
		}
		return nil, fmt.Errorf("stripe: %s", msg)
	}
	return out, nil
}

// CheckoutURL starts a subscription purchase for a plan.
func (s *Stripe) CheckoutURL(ctx context.Context, org domain.Org, email string, plan domain.Plan, successURL, cancelURL string) (string, error) {
	if !s.Configured() {
		return "", ErrNotConfigured
	}
	price := s.Prices[string(plan)]
	if price == "" {
		return "", fmt.Errorf("plan %q is not available for purchase", plan)
	}
	f := url.Values{
		"mode":                                {"subscription"},
		"line_items[0][price]":                {price},
		"line_items[0][quantity]":             {"1"},
		"success_url":                         {successURL},
		"cancel_url":                          {cancelURL},
		"client_reference_id":                 {org.ID},
		"metadata[org_id]":                    {org.ID},
		"metadata[plan]":                      {string(plan)},
		"subscription_data[metadata][org_id]": {org.ID},
		"subscription_data[metadata][plan]":   {string(plan)},
	}
	if org.Billing.CustomerID != "" {
		f.Set("customer", org.Billing.CustomerID)
	} else if email != "" {
		f.Set("customer_email", email)
	}
	out, err := s.call(ctx, "/v1/checkout/sessions", f)
	if err != nil {
		return "", err
	}
	u, _ := out["url"].(string)
	if u == "" {
		return "", errors.New("stripe returned no checkout URL")
	}
	return u, nil
}

// PortalURL opens Stripe's customer portal (change plan, payment method, cancel).
func (s *Stripe) PortalURL(ctx context.Context, customerID, returnURL string) (string, error) {
	if !s.Configured() {
		return "", ErrNotConfigured
	}
	if customerID == "" {
		return "", errors.New("no subscription yet")
	}
	out, err := s.call(ctx, "/v1/billing_portal/sessions", url.Values{"customer": {customerID}, "return_url": {returnURL}})
	if err != nil {
		return "", err
	}
	u, _ := out["url"].(string)
	return u, nil
}

// VerifyWebhook checks the Stripe-Signature header (HMAC-SHA256 over "t.payload").
func (s *Stripe) VerifyWebhook(payload []byte, header string, now time.Time) error {
	if s.WebhookSecret == "" {
		return errors.New("webhook secret not configured")
	}
	var ts string
	var sigs []string
	for _, part := range strings.Split(header, ",") {
		k, v, _ := strings.Cut(strings.TrimSpace(part), "=")
		switch k {
		case "t":
			ts = v
		case "v1":
			sigs = append(sigs, v)
		}
	}
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || len(sigs) == 0 {
		return errors.New("malformed signature header")
	}
	if d := now.Sub(time.Unix(sec, 0)); d > 5*time.Minute || d < -5*time.Minute {
		return errors.New("signature timestamp outside tolerance")
	}
	m := hmac.New(sha256.New, []byte(s.WebhookSecret))
	m.Write([]byte(ts + "."))
	m.Write(payload)
	want := m.Sum(nil)
	for _, sig := range sigs {
		if got, err := hex.DecodeString(sig); err == nil && hmac.Equal(got, want) {
			return nil
		}
	}
	return errors.New("signature mismatch")
}

// Event is the part of a Stripe webhook event we act on.
type Event struct {
	Type   string `json:"type"`
	Object struct {
		ID                string            `json:"id"`
		Status            string            `json:"status"`
		Customer          string            `json:"customer"`
		Subscription      string            `json:"subscription"`
		ClientReferenceID string            `json:"client_reference_id"`
		Metadata          map[string]string `json:"metadata"`
	}
}

func ParseEvent(payload []byte) (Event, error) {
	var raw struct {
		Type string `json:"type"`
		Data struct {
			Object json.RawMessage `json:"object"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		return Event{}, err
	}
	e := Event{Type: raw.Type}
	return e, json.Unmarshal(raw.Data.Object, &e.Object)
}

// Apply updates an org from a subscription-related event and reports whether
// the event was relevant. It is idempotent: it sets state, never increments.
func Apply(o *domain.Org, e Event) bool {
	plan := domain.Plan(e.Object.Metadata["plan"])
	valid := plan == domain.PlanStarter || plan == domain.PlanGrowth || plan == domain.PlanPro
	switch e.Type {
	case "checkout.session.completed":
		if !valid {
			return false
		}
		o.Plan, o.TrialEndsAt = plan, nil
		o.Billing = domain.Billing{CustomerID: e.Object.Customer, SubscriptionID: e.Object.Subscription, Status: "active"}
		return true
	case "customer.subscription.updated", "customer.subscription.deleted":
		if o.Billing.SubscriptionID != "" && e.Object.ID != o.Billing.SubscriptionID {
			return false // a different (old) subscription
		}
		status := e.Object.Status
		if e.Type == "customer.subscription.deleted" {
			status = "canceled"
		}
		o.Billing.Status = status
		if o.Billing.CustomerID == "" {
			o.Billing.CustomerID = e.Object.Customer
		}
		if o.Billing.SubscriptionID == "" {
			o.Billing.SubscriptionID = e.Object.ID
		}
		if valid && (status == "active" || status == "trialing") {
			o.Plan = plan
		}
		return true
	}
	return false
}
