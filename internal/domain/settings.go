package domain

import "time"

// OrgSettings holds an organization's own connections. Each customer brings
// their own email / WhatsApp / integration credentials; the platform never
// sends from a shared account. Secret fields are encrypted at rest and are
// never returned by the API (see Redact).
type OrgSettings struct {
	Email           *EmailSettings    `json:"email,omitempty"`
	WhatsApp        *WhatsAppSettings `json:"whatsapp,omitempty"`
	SenderAddress   string            `json:"sender_address,omitempty"` // postal address shown in the email footer
	NotifyEmails    []string          `json:"notify_emails,omitempty"`  // sales team, alerted when a lead qualifies
	Integrations    Integrations      `json:"integrations"`
	GooglePlacesKey string            `json:"google_places_key,omitempty"` // secret
}

type EmailSettings struct {
	FromName    string        `json:"from_name"`
	FromAddress string        `json:"from_address"`
	SMTP        SMTPSettings  `json:"smtp"`
	IMAP        *IMAPSettings `json:"imap,omitempty"` // enables automatic reply detection
}

type SMTPSettings struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password,omitempty"` // secret
	Security string `json:"security"`           // starttls | tls | none
}

type IMAPSettings struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password,omitempty"` // secret
	Security string `json:"security"`           // tls | starttls
	Folder   string `json:"folder,omitempty"`   // default INBOX
}

type WhatsAppSettings struct {
	PhoneNumberID string `json:"phone_number_id"`
	AccessToken   string `json:"access_token,omitempty"` // secret
	AppSecret     string `json:"app_secret,omitempty"`   // secret; verifies inbound webhook signatures
	VerifyToken   string `json:"verify_token,omitempty"` // secret; webhook subscription handshake
	// Business-initiated WhatsApp messages must use an approved template with
	// one body variable; free text is only allowed within 24h of the
	// customer's last message.
	TemplateName string `json:"template_name,omitempty"`
	TemplateLang string `json:"template_lang,omitempty"`
}

type Integrations struct {
	WebhookURL    string `json:"webhook_url,omitempty"`
	WebhookSecret string `json:"webhook_secret,omitempty"` // secret; signs payloads
	HubSpotToken  string `json:"hubspot_token,omitempty"`  // secret
}

// SecretsSet reports which secret fields are configured, for display.
type SecretsSet map[string]bool

// Redact returns a copy safe to return to the browser, plus which secrets are set.
func (s OrgSettings) Redact() (OrgSettings, SecretsSet) {
	set := SecretsSet{}
	mark := func(k, v string) string { set[k] = v != ""; return "" }
	if s.Email != nil {
		e := *s.Email
		e.SMTP.Password = mark("smtp_password", e.SMTP.Password)
		if e.IMAP != nil {
			i := *e.IMAP
			i.Password = mark("imap_password", i.Password)
			e.IMAP = &i
		}
		s.Email = &e
	}
	if s.WhatsApp != nil {
		w := *s.WhatsApp
		w.AccessToken = mark("whatsapp_access_token", w.AccessToken)
		w.AppSecret = mark("whatsapp_app_secret", w.AppSecret)
		w.VerifyToken = mark("whatsapp_verify_token", w.VerifyToken)
		s.WhatsApp = &w
	}
	s.Integrations.WebhookSecret = mark("webhook_secret", s.Integrations.WebhookSecret)
	s.Integrations.HubSpotToken = mark("hubspot_token", s.Integrations.HubSpotToken)
	s.GooglePlacesKey = mark("google_places_key", s.GooglePlacesKey)
	return s, set
}

// MergeSecrets keeps existing secrets wherever the update leaves them blank,
// so the UI can resubmit a form without ever having seen the secrets.
func (s OrgSettings) MergeSecrets(old OrgSettings) OrgSettings {
	keep := func(new, old string) string {
		if new == "" {
			return old
		}
		return new
	}
	if s.Email != nil && old.Email != nil {
		s.Email.SMTP.Password = keep(s.Email.SMTP.Password, old.Email.SMTP.Password)
		if s.Email.IMAP != nil && old.Email.IMAP != nil {
			s.Email.IMAP.Password = keep(s.Email.IMAP.Password, old.Email.IMAP.Password)
		}
	}
	if s.WhatsApp != nil && old.WhatsApp != nil {
		s.WhatsApp.AccessToken = keep(s.WhatsApp.AccessToken, old.WhatsApp.AccessToken)
		s.WhatsApp.AppSecret = keep(s.WhatsApp.AppSecret, old.WhatsApp.AppSecret)
		s.WhatsApp.VerifyToken = keep(s.WhatsApp.VerifyToken, old.WhatsApp.VerifyToken)
	}
	s.Integrations.WebhookSecret = keep(s.Integrations.WebhookSecret, old.Integrations.WebhookSecret)
	s.Integrations.HubSpotToken = keep(s.Integrations.HubSpotToken, old.Integrations.HubSpotToken)
	s.GooglePlacesKey = keep(s.GooglePlacesKey, old.GooglePlacesKey)
	return s
}

// APIKey grants programmatic access to one organization (Pro and above).
type APIKey struct {
	ID        string    `json:"id"`
	OrgID     string    `json:"org_id"`
	Name      string    `json:"name"`
	Prefix    string    `json:"prefix"` // first characters, to recognise the key
	Hash      string    `json:"-"`      // sha256 of the full key; the key itself is shown once
	CreatedAt time.Time `json:"created_at"`
}
