// Package webfetch downloads public web pages politely and safely: it only
// connects to public addresses, honours robots.txt, and limits size and time.
package webfetch

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/hussein/ai-salesperson/internal/netguard"
)

const UserAgent = "AISalespersonBot/1.0 (+business research; honours robots.txt)"

var (
	ErrInvalid = errors.New("invalid url")
	ErrBlocked = errors.New("blocked by robots.txt")

	reScript = regexp.MustCompile(`(?is)<(script|style|noscript)[^>]*>.*?</(script|style|noscript)>`)
	reTag    = regexp.MustCompile(`(?s)<[^>]+>`)
	reSpace  = regexp.MustCompile(`\s+`)
	reEmail  = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9\-]+(?:\.[A-Za-z0-9\-]+)*\.[A-Za-z]{2,}`)
)

type Client struct{ http *http.Client }

func New(allowPrivate bool) *Client {
	return &Client{http: &http.Client{
		Timeout:   15 * time.Second,
		Transport: &http.Transport{DialContext: netguard.Dialer(allowPrivate).DialContext},
		CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("too many redirects")
			}
			return nil
		},
	}}
}

func parse(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw != "" && !strings.Contains(raw, "://") {
		raw = "https://" + raw // directories often list bare domains
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("%w: must be an http(s) URL", ErrInvalid)
	}
	return u, nil
}

// HTML fetches a page's raw HTML (first 512KB), unless robots.txt disallows it.
func (c *Client) HTML(ctx context.Context, raw string) (string, error) {
	u, err := parse(raw)
	if err != nil {
		return "", err
	}
	if !c.robotsAllowed(ctx, u) {
		return "", ErrBlocked
	}
	b, err := c.get(ctx, u.String(), 512<<10)
	return string(b), err
}

func (c *Client) get(ctx context.Context, u string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", UserAgent)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

// robotsAllowed implements the common subset of robots.txt: Disallow rules in
// the "*" group. A missing or unreadable robots.txt allows access.
func (c *Client) robotsAllowed(ctx context.Context, u *url.URL) bool {
	b, err := c.get(ctx, u.Scheme+"://"+u.Host+"/robots.txt", 128<<10)
	if err != nil {
		return true
	}
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	inStar := false
	for _, line := range strings.Split(string(b), "\n") {
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k, v = strings.ToLower(strings.TrimSpace(k)), strings.TrimSpace(v)
		switch k {
		case "user-agent":
			inStar = v == "*"
		case "disallow":
			if inStar && v != "" && strings.HasPrefix(path, v) {
				return false
			}
		}
	}
	return true
}

// Text reduces HTML to its visible text, capped at 20k characters.
func Text(h string) string {
	t := reScript.ReplaceAllString(h, " ")
	t = reTag.ReplaceAllString(t, " ")
	t = strings.TrimSpace(reSpace.ReplaceAllString(html.UnescapeString(t), " "))
	if len(t) > 20000 {
		t = t[:20000]
	}
	return t
}

var junkEmail = regexp.MustCompile(`(?i)(\.(png|jpe?g|gif|svg|webp|css|js)$|^(user|name|email|you|your|example|test)@|@(example|domain|sentry|wixpress|godaddy)\.)`)

// Emails returns publicly listed email addresses found in a page, best first:
// addresses on the site's own domain (or a parent of it) come before others.
func Emails(h, siteHost string) []string {
	h = html.UnescapeString(h)
	seen := map[string]bool{}
	var own, other []string
	base := strings.TrimPrefix(strings.ToLower(siteHost), "www.")
	for _, m := range reEmail.FindAllString(h, -1) {
		m = strings.ToLower(strings.Trim(m, ".,;:"))
		if seen[m] || junkEmail.MatchString(m) {
			continue
		}
		seen[m] = true
		if strings.HasSuffix(m, "@"+base) || (base != "" && strings.HasSuffix(base, m[strings.Index(m, "@")+1:])) {
			own = append(own, m)
		} else {
			other = append(other, m)
		}
	}
	return append(own, other...)
}
