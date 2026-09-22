// Package inbound turns the customer's incoming mail into replies for the
// sales pipeline: it reads their IMAP mailbox and parses messages.
package inbound

import (
	"errors"
	"html"
	"io"
	"net/mail"
	"regexp"
	"strings"

	"github.com/emersion/go-message"
	_ "github.com/emersion/go-message/charset" // decode non-UTF-8 mail
	gomail "github.com/emersion/go-message/mail"
)

type Email struct {
	From    string // address only
	Subject string
	Text    string
	Auto    bool // bounce, out-of-office, list mail: never answered
}

var (
	reHTMLTag   = regexp.MustCompile(`(?s)<(script|style)[^>]*>.*?</(script|style)>|<[^>]+>`)
	reQuoteCut  = regexp.MustCompile(`(?is)\n[ \t]*on\s.{5,300}?wrote:[ \t]*\n|\n[ \t]*-{2,}\s*original message\s*-{2,}|\n[ \t]*from:[^\n]+\n[ \t]*(sent|date):|\n[ \t]*_{5,}\n`)
	reSpaceRuns = regexp.MustCompile(`[ \t]+\n`)
)

// ParseEmail extracts sender, subject and the new text of a message (without
// quoted history), and flags automatic mail.
func ParseEmail(raw []byte) (Email, error) {
	mr, err := gomail.CreateReader(strings.NewReader(string(raw)))
	if err != nil && !message.IsUnknownCharset(err) && !message.IsUnknownEncoding(err) {
		return Email{}, err
	}
	if mr == nil {
		return Email{}, errors.New("unreadable message")
	}
	var e Email
	if addrs, err := mr.Header.AddressList("From"); err == nil && len(addrs) > 0 {
		e.From = addrs[0].Address
	}
	e.Subject, _ = mr.Header.Subject()
	h := mr.Header
	if v := strings.ToLower(h.Get("Auto-Submitted")); v != "" && v != "no" {
		e.Auto = true
	}
	if h.Get("X-Autoreply") != "" || h.Get("X-Autorespond") != "" || h.Get("List-Id") != "" {
		e.Auto = true
	}
	switch strings.ToLower(h.Get("Precedence")) {
	case "bulk", "junk", "auto_reply", "list":
		e.Auto = true
	}
	if ct, _, _ := h.ContentType(); strings.HasPrefix(ct, "multipart/report") {
		e.Auto = true
	}
	local := strings.ToLower(e.From)
	if i := strings.Index(local, "@"); i >= 0 {
		local = local[:i]
	}
	if local == "mailer-daemon" || local == "postmaster" || strings.HasPrefix(local, "noreply") || strings.HasPrefix(local, "no-reply") {
		e.Auto = true
	}

	var plain, htmlBody string
	for {
		part, err := mr.NextPart()
		if err == io.EOF || (err != nil && plain != "" || err != nil && htmlBody != "") {
			break
		}
		if err != nil {
			if message.IsUnknownCharset(err) || message.IsUnknownEncoding(err) {
				continue
			}
			break
		}
		ih, ok := part.Header.(*gomail.InlineHeader)
		if !ok {
			continue // attachment
		}
		ct, _, _ := ih.ContentType()
		b, _ := io.ReadAll(io.LimitReader(part.Body, 256<<10))
		switch ct {
		case "text/plain":
			if plain == "" {
				plain = string(b)
			}
		case "text/html":
			if htmlBody == "" {
				htmlBody = string(b)
			}
		}
	}
	text := plain
	if text == "" && htmlBody != "" {
		text = html.UnescapeString(reHTMLTag.ReplaceAllString(strings.NewReplacer("<br>", "\n", "<br/>", "\n", "</p>", "\n", "</div>", "\n").Replace(htmlBody), ""))
	}
	e.Text = StripQuoted(text)
	return e, nil
}

// StripQuoted removes quoted reply history so the AI sees only what the
// prospect just wrote.
func StripQuoted(text string) string {
	t := "\n" + strings.ReplaceAll(text, "\r\n", "\n")
	if loc := reQuoteCut.FindStringIndex(t); loc != nil {
		t = t[:loc[0]]
	}
	var keep []string
	for _, ln := range strings.Split(t, "\n") {
		if strings.HasPrefix(strings.TrimSpace(ln), ">") {
			break
		}
		keep = append(keep, ln)
	}
	out := strings.TrimSpace(reSpaceRuns.ReplaceAllString(strings.Join(keep, "\n"), "\n"))
	if out == "" {
		return strings.TrimSpace(text)
	}
	return out
}

// Address extracts the bare address from a header value like `Ann <a@b.c>`.
func Address(s string) string {
	if a, err := mail.ParseAddress(s); err == nil {
		return a.Address
	}
	return strings.TrimSpace(s)
}
