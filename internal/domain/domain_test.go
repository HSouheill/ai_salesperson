package domain

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func fullSettings() OrgSettings {
	return OrgSettings{
		Email:           &EmailSettings{FromAddress: "a@b.test", SMTP: SMTPSettings{Host: "smtp", Password: "smtp-pw"}, IMAP: &IMAPSettings{Host: "imap", Password: "imap-pw"}},
		WhatsApp:        &WhatsAppSettings{PhoneNumberID: "1", AccessToken: "wa-token", AppSecret: "wa-app", VerifyToken: "wa-verify"},
		Integrations:    Integrations{WebhookURL: "https://h.test", WebhookSecret: "hook-secret", HubSpotToken: "hs"},
		GooglePlacesKey: "gp-key",
	}
}

func TestRedactNeverLeaksSecrets(t *testing.T) {
	orig := fullSettings()
	red, set := orig.Redact()
	b, _ := json.Marshal(red)
	for _, secret := range []string{"smtp-pw", "imap-pw", "wa-token", "wa-app", "wa-verify", "hook-secret", `"hs"`, "gp-key"} {
		if strings.Contains(string(b), secret) {
			t.Errorf("redacted settings still contain %s: %s", secret, b)
		}
	}
	if len(set) != 8 {
		t.Errorf("want 8 secrets reported as set, got %v", set)
	}
	// Redacting must not mutate the original (it is shared with the store).
	if orig.Email.SMTP.Password != "smtp-pw" || orig.WhatsApp.AccessToken != "wa-token" {
		t.Error("Redact modified its receiver")
	}
	if !set["smtp_password"] || !set["hubspot_token"] {
		t.Errorf("set = %v", set)
	}
}

func TestMergeSecretsKeepsBlankAndReplacesGiven(t *testing.T) {
	old := fullSettings()
	form, _ := old.Redact() // what the browser has: no secrets
	form.Email.SMTP.Host = "smtp2"
	form.WhatsApp.AccessToken = "new-token"
	merged := form.MergeSecrets(old)
	if merged.Email.SMTP.Host != "smtp2" || merged.Email.SMTP.Password != "smtp-pw" || merged.Email.IMAP.Password != "imap-pw" {
		t.Errorf("email = %+v", merged.Email)
	}
	if merged.WhatsApp.AccessToken != "new-token" || merged.WhatsApp.AppSecret != "wa-app" || merged.Integrations.HubSpotToken != "hs" || merged.GooglePlacesKey != "gp-key" {
		t.Errorf("merged = %+v", merged)
	}
	// Removing a channel removes its secrets too.
	form.WhatsApp = nil
	if m := form.MergeSecrets(old); m.WhatsApp != nil {
		t.Error("nil channel must stay removed")
	}
}

func TestPlansAndEntitlement(t *testing.T) {
	if PlanStarter.Limits().MaxAgents != 1 || PlanStarter.Limits().AutoSend || !PlanPro.Limits().AutoSend || !PlanPro.Limits().APIAccess ||
		PlanPro.Limits().WhiteLabel || !PlanEnterprise.Limits().WhiteLabel {
		t.Error("plan feature gates wrong")
	}
	now := time.Now()
	future, past := now.Add(time.Hour), now.Add(-time.Hour)
	cases := []struct {
		o    Org
		want bool
	}{
		{Org{Plan: PlanTrial, TrialEndsAt: &future}, true},
		{Org{Plan: PlanTrial, TrialEndsAt: &past}, false},
		{Org{Plan: PlanTrial}, false},
		{Org{Plan: PlanPro}, true}, // manually assigned plan, no Stripe status
		{Org{Plan: PlanPro, Billing: Billing{Status: "active"}}, true},
		{Org{Plan: PlanPro, Billing: Billing{Status: "trialing"}}, true},
		{Org{Plan: PlanPro, Billing: Billing{Status: "past_due"}}, false},
		{Org{Plan: PlanPro, Billing: Billing{Status: "canceled"}}, false},
	}
	for i, c := range cases {
		if got := c.o.Entitled(now); got != c.want {
			t.Errorf("case %d: Entitled = %v, want %v", i, got, c.want)
		}
	}
	// Org JSON must never carry credentials or the inbound token.
	o := Org{ID: "o", InboundToken: "tok-123", Settings: fullSettings()}
	b, _ := json.Marshal(o)
	if strings.Contains(string(b), "tok-123") || strings.Contains(string(b), "smtp-pw") {
		t.Errorf("Org JSON leaks private fields: %s", b)
	}
}
