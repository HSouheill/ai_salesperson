package inbound

import (
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"

	"github.com/hussein/ai-salesperson/internal/domain"
	"github.com/hussein/ai-salesperson/internal/store"
)

const plainMail = "From: Ahmed <ahmed@abc.test>\r\nTo: sales@acme.test\r\nSubject: Re: Hello\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nHow much does it cost?\r\n\r\nOn Tue, 3 Feb 2026 at 10:00, Acme Sales <sales@acme.test> wrote:\r\n> Hi Ahmed, we build ordering apps.\r\n> Open to a call?\r\n"

func TestParseEmailStripsQuotedHistory(t *testing.T) {
	e, err := ParseEmail([]byte(plainMail))
	if err != nil {
		t.Fatal(err)
	}
	if e.From != "ahmed@abc.test" || e.Subject != "Re: Hello" || e.Text != "How much does it cost?" || e.Auto {
		t.Fatalf("parsed = %+v", e)
	}
}

func TestParseEmailMultipartHTMLAndAutoReplies(t *testing.T) {
	multi := "From: a@abc.test\r\nSubject: x\r\nContent-Type: multipart/alternative; boundary=B\r\n\r\n--B\r\nContent-Type: text/html; charset=utf-8\r\n\r\n<p>We <b>already have</b> a POS system.</p>\r\n--B--\r\n"
	e, err := ParseEmail([]byte(multi))
	if err != nil || e.Text != "We already have a POS system." {
		t.Fatalf("html fallback: %+v %v", e, err)
	}
	for name, raw := range map[string]string{
		"out-of-office": "From: a@abc.test\r\nAuto-Submitted: auto-replied\r\nSubject: OOO\r\n\r\nI am away",
		"bounce":        "From: MAILER-DAEMON@x.test\r\nSubject: Undelivered\r\n\r\nfailed",
		"bulk":          "From: news@abc.test\r\nPrecedence: bulk\r\n\r\nnews",
		"noreply":       "From: no-reply@abc.test\r\n\r\nhi",
	} {
		if e, err := ParseEmail([]byte(raw)); err != nil || !e.Auto {
			t.Errorf("%s must be flagged automatic: %+v %v", name, e, err)
		}
	}
}

type recorder struct {
	mu    sync.Mutex
	calls []string
}

func (r *recorder) HandleInbound(_ context.Context, _, channel, from, body string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, channel+"|"+from+"|"+body)
	return from == "ahmed@abc.test", nil
}
func (r *recorder) Calls() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

func startIMAP(t *testing.T) (addr string, deliver func(raw string), flags func() []imap.Flag) {
	t.Helper()
	mem := imapmemserver.New()
	u := imapmemserver.NewUser("sales@acme.test", "secret")
	if err := u.Create("INBOX", nil); err != nil {
		t.Fatal(err)
	}
	mem.AddUser(u)
	srv := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return mem.NewSession(), nil, nil
		},
		InsecureAuth: true,
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	addr = ln.Addr().String()

	dial := func() *imapclient.Client {
		c, err := imapclient.DialInsecure(addr, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := c.Login("sales@acme.test", "secret").Wait(); err != nil {
			t.Fatal(err)
		}
		return c
	}
	deliver = func(raw string) {
		c := dial()
		defer c.Close()
		cmd := c.Append("INBOX", int64(len(raw)), nil)
		cmd.Write([]byte(raw))
		cmd.Close()
		if _, err := cmd.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	flags = func() []imap.Flag {
		c := dial()
		defer c.Close()
		if _, err := c.Select("INBOX", nil).Wait(); err != nil {
			t.Fatal(err)
		}
		msgs, err := c.Fetch(imap.UIDSetNum(1), &imap.FetchOptions{Flags: true}).Collect()
		if err != nil || len(msgs) == 0 {
			t.Fatalf("fetch flags: %v", err)
		}
		return msgs[0].Flags
	}
	return
}

func TestPollerReadsOnlyNewMailAndNeverTouchesFlags(t *testing.T) {
	addr, deliver, flags := startIMAP(t)
	host, portStr, _ := net.SplitHostPort(addr)
	port := 0
	for _, c := range portStr {
		port = port*10 + int(c-'0')
	}

	st := store.NewMemory()
	org := domain.Org{ID: "o1", Name: "Acme", CreatedAt: time.Now(), Settings: domain.OrgSettings{Email: &domain.EmailSettings{
		FromAddress: "sales@acme.test",
		IMAP:        &domain.IMAPSettings{Host: host, Port: port, Username: "sales@acme.test", Password: "secret", Security: "none"},
	}}}
	if err := st.CreateOrg(context.Background(), org, domain.User{ID: "u1", OrgID: "o1", Email: "o@acme.test"}); err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	p := &Poller{Store: st, Handler: rec, AllowPrivate: true}
	poll := func() Result {
		o, _ := st.GetOrg(context.Background(), "o1")
		res, err := p.PollOrg(context.Background(), o)
		if err != nil {
			t.Fatalf("poll: %v", err)
		}
		return res
	}

	// Old mail that was already in the mailbox must never be answered.
	deliver("From: old@x.test\r\nSubject: old\r\n\r\nthis was here before we connected")
	if r := poll(); r.Read != 0 || len(rec.Calls()) != 0 {
		t.Fatalf("first poll must only set the baseline, got %+v calls=%v", r, rec.Calls())
	}

	deliver(plainMail)
	deliver("From: ooo@abc.test\r\nAuto-Submitted: auto-replied\r\nSubject: away\r\n\r\nout of office")
	r := poll()
	calls := rec.Calls()
	if r.Read != 2 || r.Matched != 1 || len(calls) != 1 || calls[0] != "email|ahmed@abc.test|How much does it cost?" {
		t.Fatalf("second poll: %+v calls=%v", r, calls)
	}
	// Nothing is processed twice.
	if r := poll(); r.Read != 0 || len(rec.Calls()) != 1 {
		t.Fatalf("third poll re-read mail: %+v calls=%v", r, rec.Calls())
	}
	// The customer's mail is untouched: nothing was marked as read.
	for _, f := range flags() {
		if strings.EqualFold(string(f), `\Seen`) {
			t.Fatal("poller marked the customer's mail as read")
		}
	}
	if err := p.Check(*org.Settings.Email.IMAP); err != nil {
		t.Fatalf("Check: %v", err)
	}
	bad := *org.Settings.Email.IMAP
	bad.Password = "wrong"
	if err := p.Check(bad); err == nil {
		t.Fatal("bad password must fail Check")
	}
}
