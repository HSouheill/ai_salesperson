package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Cloudflare Turnstile anti-abuse check on signup, to make it harder for a
// script to mass-create trial accounts and blast cold email through the
// platform's sending reputation. Off by default: with no TURNSTILE_SECRET_KEY
// configured, verifyCaptcha is a no-op and signup needs no token, exactly as
// before. Set TURNSTILE_SECRET_KEY (server) and give the dashboard
// NEXT_PUBLIC_TURNSTILE_SITE_KEY (client, see web/.env.example) to turn it on.
var turnstileVerifyURL = "https://challenges.cloudflare.com/turnstile/v0/siteverify"

// TurnstileVerifyURLForTests overrides the Cloudflare verification endpoint
// and returns the previous value, so a test can point it at a fake server.
// Not for production use.
func TurnstileVerifyURLForTests(url string) string {
	old := turnstileVerifyURL
	turnstileVerifyURL = url
	return old
}

func (a *API) verifyCaptcha(ctx context.Context, token, remoteIP string) error {
	if a.cfg.TurnstileSecretKey == "" {
		return nil
	}
	if strings.TrimSpace(token) == "" {
		return fmt.Errorf("captcha verification is required")
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	form := url.Values{"secret": {a.cfg.TurnstileSecretKey}, "response": {token}}
	if remoteIP != "" {
		form.Set("remoteip", remoteIP)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, turnstileVerifyURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("captcha verification unreachable: %w", err)
	}
	defer resp.Body.Close()
	var out struct {
		Success bool `json:"success"`
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err := json.Unmarshal(raw, &out); err != nil || !out.Success {
		return fmt.Errorf("captcha verification failed")
	}
	return nil
}
