package httpapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/hussein/ai-salesperson/internal/ai"
	"github.com/hussein/ai-salesperson/internal/inbound"
	"github.com/hussein/ai-salesperson/internal/sales"
)

// Provider callbacks. Each organization has an unguessable token in the URL;
// unknown tokens get 404 and reveal nothing.

func first(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// inboundEmail accepts a forwarded reply from an email provider's inbound
// webhook (or from the customer's own automation): JSON
// {"from","subject","text"} or a form post using the same or common field names.
func (a *API) inboundEmail(w http.ResponseWriter, r *http.Request) {
	org, err := a.store.OrgByInboundToken(r.Context(), r.PathValue("token"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	var from, text string
	ct := r.Header.Get("Content-Type")
	isForm := strings.HasPrefix(ct, "application/x-www-form-urlencoded") || strings.HasPrefix(ct, "multipart/")
	if !isForm { // JSON unless the provider clearly sent a form
		var in struct {
			From string `json:"from"`
			Text string `json:"text"`
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			writeErr(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		from, text = in.From, in.Text
	} else {
		if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
			_ = r.ParseMultipartForm(2 << 20)
		} else {
			_ = r.ParseForm()
		}
		from = first(r.FormValue("from"), r.FormValue("sender"), r.FormValue("From"))
		text = first(r.FormValue("stripped-text"), r.FormValue("text"), r.FormValue("body-plain"), r.FormValue("TextBody"), r.FormValue("Text"))
	}
	if strings.TrimSpace(from) == "" || strings.TrimSpace(text) == "" {
		writeErr(w, http.StatusBadRequest, "from and text are required")
		return
	}
	a.deliverInbound(w, r, org.ID, "email", inbound.Address(from), inbound.StripQuoted(text))
}

func (a *API) deliverInbound(w http.ResponseWriter, r *http.Request, orgID, channel, from, text string) {
	matched, err := a.sales.HandleInbound(r.Context(), orgID, channel, from, text)
	switch {
	case errors.Is(err, ai.ErrUpstream):
		// Temporary: ask the provider to retry later.
		writeErr(w, http.StatusServiceUnavailable, "temporarily unavailable")
		return
	case err != nil:
		log.Printf("inbound %s for org %s not processed: %v", channel, orgID, err)
	}
	writeJSON(w, http.StatusOK, map[string]bool{"matched": matched})
}

// whatsappVerify answers Meta's webhook subscription handshake.
func (a *API) whatsappVerify(w http.ResponseWriter, r *http.Request) {
	org, err := a.store.OrgByInboundToken(r.Context(), r.PathValue("token"))
	q := r.URL.Query()
	if err != nil || org.Settings.WhatsApp == nil || org.Settings.WhatsApp.VerifyToken == "" ||
		q.Get("hub.mode") != "subscribe" ||
		subtle.ConstantTimeCompare([]byte(q.Get("hub.verify_token")), []byte(org.Settings.WhatsApp.VerifyToken)) != 1 {
		writeErr(w, http.StatusForbidden, "verification failed")
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	_, _ = io.WriteString(w, q.Get("hub.challenge"))
}

// whatsappInbound receives WhatsApp Cloud API webhooks. Payloads are only
// accepted when they carry a valid signature made with the app secret.
func (a *API) whatsappInbound(w http.ResponseWriter, r *http.Request) {
	org, err := a.store.OrgByInboundToken(r.Context(), r.PathValue("token"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 2<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "unreadable body")
		return
	}
	wa := org.Settings.WhatsApp
	if wa == nil || wa.AppSecret == "" {
		writeErr(w, http.StatusForbidden, "set the WhatsApp app secret in Settings first")
		return
	}
	m := hmac.New(sha256.New, []byte(wa.AppSecret))
	m.Write(body)
	want := "sha256=" + hex.EncodeToString(m.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(r.Header.Get("X-Hub-Signature-256"))) {
		writeErr(w, http.StatusForbidden, "invalid signature")
		return
	}
	var payload struct {
		Entry []struct {
			Changes []struct {
				Value struct {
					Messages []struct {
						From string `json:"from"`
						Type string `json:"type"`
						Text struct {
							Body string `json:"body"`
						} `json:"text"`
					} `json:"messages"`
				} `json:"value"`
			} `json:"changes"`
		} `json:"entry"`
	}
	if json.Unmarshal(body, &payload) != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	matched := 0
	for _, e := range payload.Entry {
		for _, c := range e.Changes {
			for _, msg := range c.Value.Messages {
				if msg.Type != "text" || strings.TrimSpace(msg.Text.Body) == "" {
					continue
				}
				ok, err := a.sales.HandleInbound(r.Context(), org.ID, "whatsapp", msg.From, msg.Text.Body)
				if errors.Is(err, ai.ErrUpstream) {
					writeErr(w, http.StatusServiceUnavailable, "temporarily unavailable") // Meta retries
					return
				}
				if err != nil {
					log.Printf("whatsapp inbound for org %s not processed: %v", org.ID, err)
				}
				if ok {
					matched++
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]int{"matched": matched})
}

// unsubscribe handles the RFC 8058 one-click link. It answers 200 for an
// already-invalid or already-used token too: a mail provider retrying a
// click (or a human clicking twice) must never see an error.
func (a *API) unsubscribe(w http.ResponseWriter, r *http.Request) {
	err := a.sales.Unsubscribe(r.Context(), r.PathValue("org"), r.PathValue("prospect"), r.PathValue("token"))
	if err != nil && !errors.Is(err, sales.ErrInvalid) {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("You have been unsubscribed and will not be contacted again."))
}
