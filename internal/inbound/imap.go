package inbound

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"sort"
	"strconv"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/hussein/ai-salesperson/internal/ai"
	"github.com/hussein/ai-salesperson/internal/domain"
	"github.com/hussein/ai-salesperson/internal/netguard"
	"github.com/hussein/ai-salesperson/internal/store"
)

// Handler receives each customer reply. *sales.Service implements it.
type Handler interface {
	HandleInbound(ctx context.Context, orgID, channel, from, body string) (matched bool, err error)
}

// Poller reads new mail from each customer's own IMAP mailbox.
//
// It opens the folder read-only and remembers a UID cursor, so it never
// marks, moves or deletes the customer's mail. On first connection it starts
// from the newest message, so the existing mailbox is never answered.
type Poller struct {
	Store        store.Store
	Handler      Handler
	AllowPrivate bool
}

const maxPerPoll = 50

type Result struct {
	Read    int // messages examined
	Matched int // replies from known prospects
}

func (p *Poller) dial(cfg domain.IMAPSettings) (*imapclient.Client, error) {
	port := cfg.Port
	if port == 0 {
		port = map[string]int{"starttls": 143, "none": 143}[cfg.Security]
		if port == 0 {
			port = 993
		}
	}
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(port))
	opts := &imapclient.Options{Dialer: netguard.Dialer(p.AllowPrivate), TLSConfig: &tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12}}
	switch cfg.Security {
	case "starttls":
		return imapclient.DialStartTLS(addr, opts)
	case "none":
		if !p.AllowPrivate {
			return nil, errors.New("unencrypted IMAP is not allowed")
		}
		return imapclient.DialInsecure(addr, opts)
	default:
		return imapclient.DialTLS(addr, opts)
	}
}

// Check verifies host and credentials without reading any mail.
func (p *Poller) Check(cfg domain.IMAPSettings) error {
	c, err := p.dial(cfg)
	if err != nil {
		return fmt.Errorf("connect to IMAP server: %w", err)
	}
	defer c.Close()
	if err := c.Login(cfg.Username, cfg.Password).Wait(); err != nil {
		return fmt.Errorf("IMAP login failed: %w", err)
	}
	return c.Logout().Wait()
}

func (p *Poller) PollOrg(ctx context.Context, org domain.Org) (Result, error) {
	var res Result
	if org.Settings.Email == nil || org.Settings.Email.IMAP == nil {
		return res, nil
	}
	cfg := *org.Settings.Email.IMAP
	own := org.Settings.Email.FromAddress
	c, err := p.dial(cfg)
	if err != nil {
		return res, fmt.Errorf("connect to IMAP server: %w", err)
	}
	defer c.Close()
	// Bound the whole poll, whatever the server does.
	stop := context.AfterFunc(ctx, func() { c.Close() })
	defer stop()
	if err := c.Login(cfg.Username, cfg.Password).Wait(); err != nil {
		return res, fmt.Errorf("IMAP login failed: %w", err)
	}
	folder := cfg.Folder
	if folder == "" {
		folder = "INBOX"
	}
	sel, err := c.Select(folder, &imap.SelectOptions{ReadOnly: true}).Wait()
	if err != nil {
		return res, fmt.Errorf("open folder %q: %w", folder, err)
	}

	cur := org.InboxCursor
	if cur.UIDValidity != sel.UIDValidity {
		// First connection, or the server renumbered the mailbox: start from now.
		base := uint32(0)
		if sel.UIDNext > 0 {
			base = uint32(sel.UIDNext) - 1
		}
		return res, p.Store.SetInboxCursor(ctx, org.ID, domain.InboxCursor{UIDValidity: sel.UIDValidity, LastUID: base})
	}

	found, err := c.UIDSearch(&imap.SearchCriteria{UID: []imap.UIDSet{{{Start: imap.UID(cur.LastUID) + 1, Stop: 0}}}}, nil).Wait()
	if err != nil {
		return res, fmt.Errorf("search mailbox: %w", err)
	}
	var uids []imap.UID
	for _, u := range found.AllUIDs() {
		if uint32(u) > cur.LastUID { // "n:*" always includes the last message
			uids = append(uids, u)
		}
	}
	if len(uids) == 0 {
		return res, nil
	}
	sort.Slice(uids, func(i, j int) bool { return uids[i] < uids[j] })
	if len(uids) > maxPerPoll {
		uids = uids[:maxPerPoll]
	}

	section := &imap.FetchItemBodySection{Peek: true}
	msgs, err := c.Fetch(imap.UIDSetNum(uids...), &imap.FetchOptions{UID: true, BodySection: []*imap.FetchItemBodySection{section}}).Collect()
	if err != nil {
		return res, fmt.Errorf("fetch mail: %w", err)
	}
	sort.Slice(msgs, func(i, j int) bool { return msgs[i].UID < msgs[j].UID })

	last := cur.LastUID
	var procErr error
	for _, m := range msgs {
		res.Read++
		raw := m.FindBodySection(section)
		if e, perr := ParseEmail(raw); perr == nil && !e.Auto && e.From != "" && e.Text != "" && !equalAddr(e.From, own) {
			matched, herr := p.Handler.HandleInbound(ctx, org.ID, "email", e.From, e.Text)
			if matched {
				res.Matched++
			}
			if herr != nil {
				if errors.Is(herr, ai.ErrUpstream) || errors.Is(herr, context.Canceled) || errors.Is(herr, context.DeadlineExceeded) {
					procErr = herr // temporary: keep the cursor here and retry next poll
					break
				}
				log.Printf("inbox %s: reply from %s not processed: %v", org.ID, e.From, herr)
			}
		}
		last = uint32(m.UID)
	}
	if last != cur.LastUID {
		if err := p.Store.SetInboxCursor(context.WithoutCancel(ctx), org.ID, domain.InboxCursor{UIDValidity: sel.UIDValidity, LastUID: last}); err != nil {
			return res, err
		}
	}
	_ = c.Logout().Wait()
	return res, procErr
}

func equalAddr(a, b string) bool {
	return len(a) == len(b) && (a == b || lower(a) == lower(b))
}

func lower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}
