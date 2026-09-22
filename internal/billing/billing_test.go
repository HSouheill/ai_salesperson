package billing

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hussein/ai-salesperson/internal/domain"
)

func sign(secret string, ts time.Time, payload string) string {
	t := strconv.FormatInt(ts.Unix(), 10)
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(t + "." + payload))
	return "t=" + t + ",v1=" + hex.EncodeToString(m.Sum(nil))
}

func TestVerifyWebhook(t *testing.T) {
	s := &Stripe{WebhookSecret: "whsec"}
	now := time.Now()
	body := []byte(`{"type":"x"}`)
	if err := s.VerifyWebhook(body, sign("whsec", now, string(body)), now); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}
	cases := map[string]string{
		"wrong secret":  sign("other", now, string(body)),
		"tampered body": sign("whsec", now, `{"type":"y"}`),
		"stale":         sign("whsec", now.Add(-time.Hour), string(body)),
		"garbage":       "nonsense",
		"empty":         "",
	}
	for name, h := range cases {
		if err := s.VerifyWebhook(body, h, now); err == nil {
			t.Errorf("%s must be rejected", name)
		}
	}
	if err := (&Stripe{}).VerifyWebhook(body, sign("", now, string(body)), now); err == nil {
		t.Error("an unset webhook secret must reject everything, not accept an empty-key signature")
	}
}

func TestApplyTransitions(t *testing.T) {
	ends := time.Now().Add(time.Hour)
	o := domain.Org{ID: "o1", Plan: domain.PlanTrial, TrialEndsAt: &ends}

	e, _ := ParseEvent([]byte(`{"type":"checkout.session.completed","data":{"object":{"customer":"cus_1","subscription":"sub_1","client_reference_id":"o1","metadata":{"org_id":"o1","plan":"pro"}}}}`))
	if !Apply(&o, e) || o.Plan != domain.PlanPro || o.TrialEndsAt != nil || o.Billing.CustomerID != "cus_1" || o.Billing.SubscriptionID != "sub_1" || o.Billing.Status != "active" {
		t.Fatalf("after checkout: %+v", o)
	}
	if !o.Entitled(time.Now()) {
		t.Fatal("paid org must be entitled")
	}

	// An event about some other (old) subscription must not change this org.
	old, _ := ParseEvent([]byte(`{"type":"customer.subscription.deleted","data":{"object":{"id":"sub_OLD","metadata":{"org_id":"o1"}}}}`))
	if Apply(&o, old) || o.Billing.Status != "active" {
		t.Fatalf("stale subscription event applied: %+v", o.Billing)
	}

	pd, _ := ParseEvent([]byte(`{"type":"customer.subscription.updated","data":{"object":{"id":"sub_1","status":"past_due","metadata":{"org_id":"o1","plan":"pro"}}}}`))
	Apply(&o, pd)
	if o.Entitled(time.Now()) {
		t.Fatal("past_due must stop paid work")
	}
	up, _ := ParseEvent([]byte(`{"type":"customer.subscription.updated","data":{"object":{"id":"sub_1","status":"active","metadata":{"org_id":"o1","plan":"growth"}}}}`))
	Apply(&o, up)
	if o.Plan != domain.PlanGrowth || !o.Entitled(time.Now()) {
		t.Fatalf("plan change: %+v", o)
	}
	del, _ := ParseEvent([]byte(`{"type":"customer.subscription.deleted","data":{"object":{"id":"sub_1","metadata":{"org_id":"o1"}}}}`))
	Apply(&o, del)
	if o.Billing.Status != "canceled" || o.Entitled(time.Now()) {
		t.Fatalf("canceled: %+v", o)
	}

	// A checkout for a plan we don't sell (metadata tampering) is ignored.
	fresh := domain.Org{ID: "o2", Plan: domain.PlanTrial}
	bad, _ := ParseEvent([]byte(`{"type":"checkout.session.completed","data":{"object":{"metadata":{"plan":"enterprise"}}}}`))
	if Apply(&fresh, bad) || fresh.Plan != domain.PlanTrial {
		t.Fatal("enterprise must not be obtainable through checkout metadata")
	}
}

func TestCheckoutAndPortalRequests(t *testing.T) {
	var form url.Values
	var path, auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		form, _ = url.ParseQuery(string(b))
		path, auth = r.URL.Path, r.Header.Get("Authorization")
		if strings.Contains(r.URL.Path, "sessions") && form.Get("customer") == "cus_bad" {
			w.WriteHeader(400)
			w.Write([]byte(`{"error":{"message":"No such customer"}}`))
			return
		}
		w.Write([]byte(`{"url":"https://stripe.test/pay"}`))
	}))
	defer srv.Close()
	s := &Stripe{Key: "sk_test", Base: srv.URL, Prices: map[string]string{"pro": "price_pro"}}

	u, err := s.CheckoutURL(context.Background(), domain.Org{ID: "o1"}, "me@acme.test", domain.PlanPro, "https://w/ok", "https://w/no")
	if err != nil || u != "https://stripe.test/pay" {
		t.Fatal(u, err)
	}
	for k, want := range map[string]string{"mode": "subscription", "line_items[0][price]": "price_pro", "client_reference_id": "o1",
		"metadata[plan]": "pro", "subscription_data[metadata][org_id]": "o1", "customer_email": "me@acme.test", "success_url": "https://w/ok"} {
		if form.Get(k) != want {
			t.Errorf("%s = %q, want %q", k, form.Get(k), want)
		}
	}
	if path != "/v1/checkout/sessions" || auth != "Bearer sk_test" {
		t.Errorf("path=%s auth=%s", path, auth)
	}
	if _, err := s.CheckoutURL(context.Background(), domain.Org{ID: "o1"}, "", domain.PlanGrowth, "a", "b"); err == nil {
		t.Error("a plan without a configured price must be refused")
	}
	if _, err := s.CheckoutURL(context.Background(), domain.Org{ID: "o1", Billing: domain.Billing{CustomerID: "cus_bad"}}, "", domain.PlanPro, "a", "b"); err == nil ||
		!strings.Contains(err.Error(), "No such customer") {
		t.Errorf("stripe errors should surface: %v", err)
	}
	if _, err := (&Stripe{}).CheckoutURL(context.Background(), domain.Org{}, "", domain.PlanPro, "a", "b"); err != ErrNotConfigured {
		t.Errorf("unconfigured => ErrNotConfigured, got %v", err)
	}
	if _, err := s.PortalURL(context.Background(), "cus_1", "https://w"); err != nil || path != "/v1/billing_portal/sessions" {
		t.Errorf("portal: %v %s", err, path)
	}
}
