package httpapi

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/hussein/ai-salesperson/internal/channels"
	"github.com/hussein/ai-salesperson/internal/domain"
)

func (a *API) inboundURLs(o domain.Org) map[string]string {
	base := strings.TrimRight(a.cfg.PublicURL, "/") + "/v1/inbound/"
	return map[string]string{"email": base + "email/" + o.InboundToken, "whatsapp": base + "whatsapp/" + o.InboundToken}
}

func (a *API) getSettings(w http.ResponseWriter, r *http.Request) {
	o, err := a.store.GetOrg(r.Context(), orgID(r))
	if err != nil {
		fail(w, err)
		return
	}
	red, set := o.Settings.Redact()
	_, emailErr := a.sales.Channels.For(o, "email")
	_, waErr := a.sales.Channels.For(o, "whatsapp")
	writeJSON(w, http.StatusOK, map[string]any{
		"settings": red, "secrets_set": set, "inbound_urls": a.inboundURLs(o),
		"channels":             map[string]bool{"email": emailErr == nil, "whatsapp": waErr == nil},
		"integrations_allowed": o.Plan.Limits().Integrations,
	})
}

func validHost(h string) bool {
	return h != "" && len(h) <= 255 && !strings.ContainsAny(h, " /\\@:\t\r\n")
}

func (a *API) validateSettings(s domain.OrgSettings, lim domain.Limits) error {
	bad := func(m string) error { return errors.New(m) }
	if e := s.Email; e != nil {
		if _, ok := validEmail(e.FromAddress); !ok {
			return bad("email: from_address must be a valid email address")
		}
		if len(e.FromName) > 100 {
			return bad("email: from_name too long")
		}
		if !validHost(e.SMTP.Host) || e.SMTP.Port < 0 || e.SMTP.Port > 65535 {
			return bad("email: SMTP host/port invalid")
		}
		switch e.SMTP.Security {
		case "", "starttls", "tls":
		case "none":
			if !a.cfg.AllowPrivateNet {
				return bad("email: unencrypted SMTP is not allowed; use starttls or tls")
			}
		default:
			return bad("email: SMTP security must be starttls, tls or none")
		}
		if i := e.IMAP; i != nil {
			if !validHost(i.Host) || i.Port < 0 || i.Port > 65535 || i.Username == "" {
				return bad("email: IMAP host/port/username invalid")
			}
			switch i.Security {
			case "", "tls", "starttls":
			case "none":
				if !a.cfg.AllowPrivateNet {
					return bad("email: unencrypted IMAP is not allowed; use tls or starttls")
				}
			default:
				return bad("email: IMAP security must be tls or starttls")
			}
		}
	}
	if w := s.WhatsApp; w != nil {
		if w.PhoneNumberID == "" || len(w.PhoneNumberID) > 40 || strings.Trim(w.PhoneNumberID, "0123456789") != "" {
			return bad("whatsapp: phone_number_id must be the numeric ID from Meta")
		}
	}
	if len(s.NotifyEmails) > 10 {
		return bad("at most 10 notification emails")
	}
	for _, e := range s.NotifyEmails {
		if _, ok := validEmail(e); !ok {
			return bad("notify_emails contains an invalid address")
		}
	}
	if len(s.SenderAddress) > 300 {
		return bad("sender_address too long")
	}
	in := s.Integrations
	if in.WebhookURL != "" || in.HubSpotToken != "" {
		if !lim.Integrations {
			return bad("CRM integrations need the Pro plan")
		}
	}
	if in.WebhookURL != "" {
		u, err := url.Parse(in.WebhookURL)
		if err != nil || u.Host == "" || (u.Scheme != "https" && !(u.Scheme == "http" && a.cfg.AllowPrivateNet)) {
			return bad("integrations: webhook_url must be an https URL")
		}
	}
	return nil
}

func sameIMAP(a, b *domain.EmailSettings) bool {
	ia, ib := imapOf(a), imapOf(b)
	if ia == nil || ib == nil {
		return ia == ib
	}
	return ia.Host == ib.Host && ia.Username == ib.Username && ia.Folder == ib.Folder
}

func imapOf(e *domain.EmailSettings) *domain.IMAPSettings {
	if e == nil {
		return nil
	}
	return e.IMAP
}

// putSettings replaces the org's connections. Secrets left blank keep their
// stored value, so the form never needs to know them.
func (a *API) putSettings(w http.ResponseWriter, r *http.Request) {
	var in domain.OrgSettings
	if !decode(w, r, &in) {
		return
	}
	o, err := a.store.GetOrg(r.Context(), orgID(r))
	if err != nil {
		fail(w, err)
		return
	}
	if err := a.validateSettings(in, o.Plan.Limits()); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	merged := in.MergeSecrets(o.Settings)
	resetInbox := !sameIMAP(o.Settings.Email, merged.Email)
	o.Settings = merged
	if err := a.store.UpdateOrg(r.Context(), o); err != nil {
		fail(w, err)
		return
	}
	if resetInbox { // a different mailbox: start reading from its newest message
		if err := a.store.SetInboxCursor(r.Context(), o.ID, domain.InboxCursor{}); err != nil {
			fail(w, err)
			return
		}
	}
	a.getSettings(w, r)
}

type checkResult struct {
	Configured bool   `json:"configured"`
	OK         bool   `json:"ok"`
	Error      string `json:"error,omitempty"`
}

// testSettings checks the saved connections without sending or reading mail.
func (a *API) testSettings(w http.ResponseWriter, r *http.Request) {
	if !a.testRL(orgID(r)) {
		writeErr(w, http.StatusTooManyRequests, "too many tests; try again in a minute")
		return
	}
	o, err := a.store.GetOrg(r.Context(), orgID(r))
	if err != nil {
		fail(w, err)
		return
	}
	var smtp, imap checkResult
	if e := o.Settings.Email; e != nil {
		smtp.Configured = true
		if err := channels.CheckSMTP(r.Context(), *e, a.cfg.AllowPrivateNet); err != nil {
			smtp.Error = err.Error()
		} else {
			smtp.OK = true
		}
		if e.IMAP != nil {
			imap.Configured = true
			if err := a.poller.Check(*e.IMAP); err != nil {
				imap.Error = err.Error()
			} else {
				imap.OK = true
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]checkResult{"smtp": smtp, "imap": imap})
}

func (a *API) rotateInboundToken(w http.ResponseWriter, r *http.Request) {
	o, err := a.store.GetOrg(r.Context(), orgID(r))
	if err != nil {
		fail(w, err)
		return
	}
	o.InboundToken = randomHex(24)
	if err := a.store.UpdateOrg(r.Context(), o); err != nil {
		fail(w, err)
		return
	}
	a.getSettings(w, r)
}
