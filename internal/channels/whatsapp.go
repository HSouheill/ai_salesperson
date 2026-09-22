package channels

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/hussein/ai-salesperson/internal/domain"
	"github.com/hussein/ai-salesperson/internal/netguard"
)

const defaultGraphBase = "https://graph.facebook.com/v21.0"

// WhatsApp sends through the customer's own WhatsApp Business Cloud API number.
type WhatsApp struct {
	cfg  domain.WhatsAppSettings
	http *http.Client
	base string
}

func NewWhatsApp(cfg domain.WhatsAppSettings, hc *http.Client, base string, allowPrivate bool) *WhatsApp {
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{DialContext: netguard.Dialer(allowPrivate).DialContext}}
	}
	if base == "" {
		base = defaultGraphBase
	}
	return &WhatsApp{cfg: cfg, http: hc, base: strings.TrimRight(base, "/")}
}

func (w *WhatsApp) Name() string { return "whatsapp" }
func (w *WhatsApp) Recipient(_, phone string) string {
	return digits(phone)
}

func digits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (w *WhatsApp) Send(ctx context.Context, o Outgoing) error {
	to := digits(o.To)
	if to == "" {
		return fmt.Errorf("invalid WhatsApp number")
	}
	payload := map[string]any{"messaging_product": "whatsapp", "to": to}
	switch {
	case o.InWindow:
		payload["type"] = "text"
		payload["text"] = map[string]any{"body": o.Body}
	case w.cfg.TemplateName != "":
		// Outside the 24h window only approved templates may be sent. The
		// template must have exactly one body variable, which carries the text.
		lang := w.cfg.TemplateLang
		if lang == "" {
			lang = "en"
		}
		body := strings.Join(strings.Fields(o.Body), " ") // templates reject newlines
		if len(body) > 1000 {
			body = body[:1000]
		}
		payload["type"] = "template"
		payload["template"] = map[string]any{
			"name": w.cfg.TemplateName, "language": map[string]any{"code": lang},
			"components": []any{map[string]any{"type": "body", "parameters": []any{map[string]any{"type": "text", "text": body}}}},
		}
	default:
		return fmt.Errorf("WhatsApp only allows first contact through an approved template; set a template name in Settings")
	}
	b, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.base+"/"+w.cfg.PhoneNumberID+"/messages", bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+w.cfg.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := w.http.Do(req)
	if err != nil {
		return fmt.Errorf("WhatsApp API unreachable: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		var e struct {
			Error struct{ Message string } `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		if e.Error.Message == "" {
			e.Error.Message = fmt.Sprintf("status %d", resp.StatusCode)
		}
		return fmt.Errorf("WhatsApp API: %s", e.Error.Message)
	}
	return nil
}
