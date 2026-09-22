package channels

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"mime/quotedprintable"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/hussein/ai-salesperson/internal/domain"
)

// fakeSMTP is a minimal SMTP server that records delivered messages.
func fakeSMTP(t *testing.T) (host string, port int, get func() []string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	var mu sync.Mutex
	var msgs []string
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				r := bufio.NewReader(c)
				w := func(s string) { io.WriteString(c, s+"\r\n") }
				w("220 fake ready")
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					up := strings.ToUpper(strings.TrimSpace(line))
					switch {
					case strings.HasPrefix(up, "EHLO"):
						io.WriteString(c, "250-fake\r\n250 AUTH PLAIN\r\n")
					case strings.HasPrefix(up, "AUTH"):
						w("235 ok")
					case strings.HasPrefix(up, "MAIL"), strings.HasPrefix(up, "RCPT"):
						w("250 ok")
					case up == "DATA":
						w("354 go")
						var b strings.Builder
						for {
							l, err := r.ReadString('\n')
							if err != nil || l == ".\r\n" {
								break
							}
							b.WriteString(l)
						}
						mu.Lock()
						msgs = append(msgs, b.String())
						mu.Unlock()
						w("250 queued")
					case up == "QUIT":
						w("221 bye")
						return
					default:
						w("250 ok")
					}
				}
			}()
		}
	}()
	h, p, _ := net.SplitHostPort(ln.Addr().String())
	pn, _ := strconv.Atoi(p)
	return h, pn, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), msgs...) }
}

func emailCfg(host string, port int) domain.EmailSettings {
	return domain.EmailSettings{FromName: "Acme Sales", FromAddress: "sales@acme.test",
		SMTP: domain.SMTPSettings{Host: host, Port: port, Username: "u", Password: "p", Security: "none"}}
}

func TestEmailSendsThroughTenantSMTP(t *testing.T) {
	host, port, got := fakeSMTP(t)
	e := NewEmail(emailCfg(host, port), true)
	if err := e.Send(context.Background(), Outgoing{To: "ahmed@abc.test", Subject: "Hello Ahmed", Body: "Hi Ahmed,\nمرحبا — we build ordering apps."}); err != nil {
		t.Fatal(err)
	}
	msgs := got()
	if len(msgs) != 1 {
		t.Fatalf("delivered %d messages", len(msgs))
	}
	m := msgs[0]
	for _, want := range []string{"From: \"Acme Sales\" <sales@acme.test>", "To: ahmed@abc.test", "Subject: Hello Ahmed", "List-Unsubscribe: <mailto:sales@acme.test?subject=unsubscribe>"} {
		if !strings.Contains(m, want) {
			t.Errorf("message missing %q:\n%s", want, m)
		}
	}
	// The body is quoted-printable UTF-8; decoding must give the original text back.
	_, body, _ := strings.Cut(m, "\r\n\r\n")
	dec, _ := io.ReadAll(quotedprintable.NewReader(strings.NewReader(body)))
	if !strings.Contains(string(dec), "مرحبا") {
		t.Errorf("non-ASCII body not preserved: %q", dec)
	}
}

func TestEmailCannotInjectHeaders(t *testing.T) {
	host, port, got := fakeSMTP(t)
	e := NewEmail(emailCfg(host, port), true)
	evil := "Nice offer\r\nBcc: victim@evil.test\r\nX-Injected: yes"
	if err := e.Send(context.Background(), Outgoing{To: "ahmed@abc.test", Subject: evil, Body: "hi"}); err != nil {
		t.Fatal(err)
	}
	headers, _, _ := strings.Cut(got()[0], "\r\n\r\n")
	for _, l := range strings.Split(headers, "\r\n") {
		if strings.HasPrefix(l, "Bcc:") || strings.HasPrefix(l, "X-Injected:") {
			t.Fatalf("header injected: %q", l)
		}
	}
	// A recipient smuggling a second address is refused outright.
	if err := e.Send(context.Background(), Outgoing{To: "a@abc.test, b@evil.test", Subject: "x", Body: "y"}); err == nil {
		t.Error("multiple recipients must be rejected")
	}
}

func TestEmailBlocksPrivateHostsUnlessAllowed(t *testing.T) {
	host, port, _ := fakeSMTP(t)
	if err := NewEmail(emailCfg(host, port), false).Send(context.Background(), Outgoing{To: "a@b.test", Subject: "s", Body: "b"}); err == nil ||
		!strings.Contains(err.Error(), "blocked non-public") {
		t.Fatalf("loopback SMTP host must be blocked for tenants, got %v", err)
	}
	if err := CheckSMTP(context.Background(), emailCfg(host, port), true); err != nil {
		t.Fatalf("CheckSMTP: %v", err)
	}
}

func TestWhatsAppTemplateRules(t *testing.T) {
	var mu sync.Mutex
	var bodies []map[string]any
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		mu.Lock()
		bodies, auth = append(bodies, b), r.Header.Get("Authorization")
		mu.Unlock()
		if r.URL.Path != "/PN1/messages" {
			http.Error(w, `{"error":{"message":"wrong path"}}`, 404)
			return
		}
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	cfg := domain.WhatsAppSettings{PhoneNumberID: "PN1", AccessToken: "tok", TemplateName: "intro", TemplateLang: "en_US"}
	w := NewWhatsApp(cfg, srv.Client(), srv.URL, true)

	// Inside the 24h window: free text.
	if err := w.Send(context.Background(), Outgoing{To: "+961 3 123 456", Body: "Sure!", InWindow: true}); err != nil {
		t.Fatal(err)
	}
	// First contact: only an approved template, with the text as its variable.
	if err := w.Send(context.Background(), Outgoing{To: "+961 3 123 456", Body: "Hello\nthere", InWindow: false}); err != nil {
		t.Fatal(err)
	}
	if bodies[0]["type"] != "text" || bodies[0]["to"] != "9613123456" || auth != "Bearer tok" {
		t.Errorf("text message wrong: %v auth=%q", bodies[0], auth)
	}
	tpl := bodies[1]["template"].(map[string]any)
	param := tpl["components"].([]any)[0].(map[string]any)["parameters"].([]any)[0].(map[string]any)["text"]
	if bodies[1]["type"] != "template" || tpl["name"] != "intro" || param != "Hello there" {
		t.Errorf("template message wrong: %v", bodies[1])
	}

	// Without a template, first contact must fail loudly instead of being rejected by Meta later.
	cfg.TemplateName = ""
	if err := NewWhatsApp(cfg, srv.Client(), srv.URL, true).Send(context.Background(), Outgoing{To: "961", Body: "x"}); err == nil {
		t.Error("first contact without a template must be refused")
	}
}

func TestTenantProvider(t *testing.T) {
	org := domain.Org{}
	if _, err := (Tenant{}).For(org, "email"); err == nil {
		t.Error("unconfigured channel must not silently deliver")
	}
	if ch, err := (Tenant{DevLog: true}).For(org, "email"); err != nil || ch.Name() != "email" {
		t.Errorf("dev log channel: %v", err)
	}
	org.Settings.Email = &domain.EmailSettings{FromAddress: "a@b.test", SMTP: domain.SMTPSettings{Host: "smtp.b.test"}}
	if _, err := (Tenant{}).For(org, "email"); err != nil {
		t.Errorf("configured email: %v", err)
	}
	if _, err := (Tenant{}).For(org, "sms"); err == nil {
		t.Error("unknown channel")
	}
}
