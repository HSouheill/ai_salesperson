package integrations

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hussein/ai-salesperson/internal/domain"
)

func orgWith(in domain.Integrations) domain.Org {
	return domain.Org{ID: "o1", Settings: domain.OrgSettings{Integrations: in}}
}

func TestWebhookIsSignedAndCarriesTheLead(t *testing.T) {
	var body []byte
	var sig, event string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		sig, event = r.Header.Get("X-AISP-Signature"), r.Header.Get("X-AISP-Event")
	}))
	defer srv.Close()

	c := New(true)
	p := domain.Prospect{ID: "p1", Name: "ABC", Contact: domain.Contact{Email: "a@abc.test"}, Score: 88, DealValue: 900}
	_, err := c.Push(context.Background(), orgWith(domain.Integrations{WebhookURL: srv.URL, WebhookSecret: "s3cret"}), domain.Agent{ID: "a1", Name: "Hunter"}, p, LeadQualified)
	if err != nil {
		t.Fatal(err)
	}
	m := hmac.New(sha256.New, []byte("s3cret"))
	m.Write(body)
	if sig != "sha256="+hex.EncodeToString(m.Sum(nil)) || event != "lead.qualified" {
		t.Errorf("sig=%q event=%q", sig, event)
	}
	var got struct {
		Event    string
		Prospect struct {
			Name      string
			DealValue float64 `json:"deal_value"`
		}
	}
	if json.Unmarshal(body, &got) != nil || got.Event != "lead.qualified" || got.Prospect.Name != "ABC" || got.Prospect.DealValue != 900 {
		t.Errorf("payload = %s", body)
	}
}

func TestWebhookFailuresAreReportedAndSSRFIsBlocked(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) }))
	defer bad.Close()
	_, err := New(true).Push(context.Background(), orgWith(domain.Integrations{WebhookURL: bad.URL}), domain.Agent{}, domain.Prospect{}, LeadQualified)
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("non-2xx must be an error, got %v", err)
	}
	// Tenants must not be able to aim webhooks at the internal network.
	_, err = New(false).Push(context.Background(), orgWith(domain.Integrations{WebhookURL: bad.URL}), domain.Agent{}, domain.Prospect{}, LeadQualified)
	if err == nil || !strings.Contains(err.Error(), "blocked non-public") {
		t.Errorf("loopback webhook must be blocked, got %v", err)
	}
}

func TestHubSpotCreateExistingAndUpdate(t *testing.T) {
	var calls []string
	var props map[string]map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer hs-token" {
			w.WriteHeader(401)
			return
		}
		json.NewDecoder(r.Body).Decode(&props)
		switch {
		case r.Method == "POST" && props["properties"]["email"] == "dup@abc.test":
			w.WriteHeader(409)
			w.Write([]byte(`{"message":"Contact already exists. Existing ID: 4242"}`))
		case r.Method == "POST":
			w.WriteHeader(201)
			w.Write([]byte(`{"id":"777"}`))
		default:
			w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()
	c := New(true)
	c.HubSpotBase = srv.URL
	org := orgWith(domain.Integrations{HubSpotToken: "hs-token"})

	id, err := c.Push(context.Background(), org, domain.Agent{}, domain.Prospect{Name: "ABC Ltd", ContactName: "Ahmed Khan", Contact: domain.Contact{Email: "a@abc.test"}}, LeadQualified)
	if err != nil || id != "777" || props["properties"]["firstname"] != "" && props["properties"]["lastname"] != "Khan" {
		t.Fatalf("create: id=%q err=%v props=%v", id, err, props)
	}
	id, err = c.Push(context.Background(), org, domain.Agent{}, domain.Prospect{Contact: domain.Contact{Email: "dup@abc.test"}}, LeadQualified)
	if err != nil || id != "4242" {
		t.Fatalf("existing contact must be reused: id=%q err=%v", id, err)
	}
	id, err = c.Push(context.Background(), org, domain.Agent{}, domain.Prospect{CRMID: "777"}, DealWon)
	if err != nil || id != "777" || calls[len(calls)-1] != "PATCH /crm/v3/objects/contacts/777" || props["properties"]["lifecyclestage"] != "customer" {
		t.Fatalf("won: id=%q err=%v calls=%v props=%v", id, err, calls, props)
	}
	if _, err := c.Push(context.Background(), org, domain.Agent{}, domain.Prospect{}, LeadQualified); err == nil {
		t.Error("a prospect without an email cannot become a HubSpot contact")
	}
}
