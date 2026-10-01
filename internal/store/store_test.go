package store

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hussein/ai-salesperson/internal/domain"
	"github.com/hussein/ai-salesperson/internal/secrets"
)

var ctx = context.Background()

// backends returns the in-memory store, plus Postgres when TEST_DATABASE_URL is set.
func backends(t *testing.T) map[string]func(t *testing.T) Store {
	m := map[string]func(t *testing.T) Store{"memory": func(*testing.T) Store { return NewMemory() }}
	if url := os.Getenv("TEST_DATABASE_URL"); url != "" {
		m["postgres"] = func(t *testing.T) Store {
			pg, err := NewPostgres(ctx, url, secrets.FromPassphrase("store-test"))
			if err != nil {
				t.Fatal(err)
			}
			pool, err := pgxpool.New(ctx, url)
			if err != nil {
				t.Fatal(err)
			}
			defer pool.Close()
			if _, err := pool.Exec(ctx, `TRUNCATE api_keys, messages, prospects, agents, users, orgs CASCADE`); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(pg.Close)
			return pg
		}
	}
	return m
}

func each(t *testing.T, f func(t *testing.T, st Store)) {
	for name, mk := range backends(t) {
		t.Run(name, func(t *testing.T) { f(t, mk(t)) })
	}
}

func mkOrg(t *testing.T, st Store, id, email string, mut func(*domain.Org)) domain.Org {
	t.Helper()
	o := domain.Org{ID: id, Name: id, Plan: domain.PlanTrial, CreatedAt: time.Now()}
	if mut != nil {
		mut(&o)
	}
	if err := st.CreateOrg(ctx, o, domain.User{ID: "u-" + id, OrgID: id, Email: email, PasswordHash: "h"}); err != nil {
		t.Fatalf("CreateOrg %s: %v", id, err)
	}
	return o
}

func TestOrgsUsersAndLookups(t *testing.T) {
	each(t, func(t *testing.T, st Store) {
		mkOrg(t, st, "agency", "boss@x.test", func(o *domain.Org) { o.InboundToken = "tok-agency" })
		mkOrg(t, st, "c1", "c1@x.test", func(o *domain.Org) { o.ParentID, o.Slug, o.InboundToken = "agency", "cedar", "tok-c1" })
		mkOrg(t, st, "c2", "c2@x.test", func(o *domain.Org) { o.ParentID = "agency" })

		// Emails are unique case-insensitively; slugs are unique.
		if err := st.CreateOrg(ctx, domain.Org{ID: "dup", Name: "d"}, domain.User{ID: "ud", OrgID: "dup", Email: "BOSS@x.test"}); !errors.Is(err, ErrConflict) {
			t.Errorf("duplicate email: %v", err)
		}
		if err := st.CreateOrg(ctx, domain.Org{ID: "dup2", Name: "d", Slug: "cedar"}, domain.User{ID: "ud2", OrgID: "dup2", Email: "new@x.test"}); !errors.Is(err, ErrConflict) {
			t.Errorf("duplicate slug: %v", err)
		}
		// A failed CreateOrg leaves nothing behind.
		if _, err := st.GetOrg(ctx, "dup"); !errors.Is(err, ErrNotFound) {
			t.Errorf("failed create left an org behind: %v", err)
		}
		if u, err := st.UserByEmail(ctx, "Boss@X.test"); err != nil || u.OrgID != "agency" {
			t.Errorf("UserByEmail: %v %+v", err, u)
		}
		if o, err := st.OrgBySlug(ctx, "cedar"); err != nil || o.ID != "c1" {
			t.Errorf("OrgBySlug: %v", err)
		}
		if o, err := st.OrgByInboundToken(ctx, "tok-c1"); err != nil || o.ID != "c1" {
			t.Errorf("OrgByInboundToken: %v", err)
		}
		for _, k := range []string{"", "nope"} {
			if _, err := st.OrgByInboundToken(ctx, k); !errors.Is(err, ErrNotFound) {
				t.Errorf("token %q: %v", k, err)
			}
		}
		if _, err := st.OrgBySlug(ctx, ""); !errors.Is(err, ErrNotFound) {
			t.Error("empty slug must not match orgs without one")
		}
		kids, _ := st.ListChildOrgs(ctx, "agency")
		if len(kids) != 2 {
			t.Errorf("children = %d", len(kids))
		}
		if none, _ := st.ListChildOrgs(ctx, ""); len(none) != 0 {
			t.Error("empty parent must list nothing (top-level orgs are not children)")
		}
	})
}

func TestListOrgsForAdmin(t *testing.T) {
	each(t, func(t *testing.T, st Store) {
		mkOrg(t, st, "acme", "owner@acme.test", func(o *domain.Org) { o.Name = "Acme Software" })
		mkOrg(t, st, "cedar", "owner@cedar.test", func(o *domain.Org) { o.Name = "Cedar Restaurants" })
		mkOrg(t, st, "agency", "boss@bright.test", func(o *domain.Org) { o.Name = "Bright Agency" })
		mkOrg(t, st, "client1", "staff@client1.test", func(o *domain.Org) { o.Name = "Agency Client"; o.ParentID = "agency" })

		if n, err := st.CountOrgs(ctx); err != nil || n != 3 {
			t.Fatalf("CountOrgs = %d, %v (client orgs must not be counted)", n, err)
		}
		all, err := st.ListOrgs(ctx, "", 10, 0)
		if err != nil || len(all) != 3 {
			t.Fatalf("ListOrgs(all) = %d, %v", len(all), err)
		}
		for _, o := range all {
			if o.ID == "client1" {
				t.Error("a client org (has a parent) must not appear in the admin list")
			}
		}
		// Search matches org name, org ID, or a user's email.
		if got, _ := st.ListOrgs(ctx, "cedar", 10, 0); len(got) != 1 || got[0].ID != "cedar" {
			t.Errorf("search by name = %v", got)
		}
		if got, _ := st.ListOrgs(ctx, "owner@acme.test", 10, 0); len(got) != 1 || got[0].ID != "acme" {
			t.Errorf("search by email = %v", got)
		}
		if got, _ := st.ListOrgs(ctx, "nobody-matches-this", 10, 0); len(got) != 0 {
			t.Errorf("search with no match = %v", got)
		}
		// Pagination.
		page1, _ := st.ListOrgs(ctx, "", 2, 0)
		page2, _ := st.ListOrgs(ctx, "", 2, 2)
		if len(page1) != 2 || len(page2) != 1 {
			t.Fatalf("pages = %d, %d", len(page1), len(page2))
		}
		if page1[0].ID == page2[0].ID {
			t.Error("pages overlap")
		}
		if empty, err := st.ListOrgs(ctx, "", 10, 100); err != nil || len(empty) != 0 {
			t.Errorf("offset past the end = %v, %v", empty, err)
		}
	})
}

func TestSettingsRoundTripAndCursorSurvivesUpdates(t *testing.T) {
	each(t, func(t *testing.T, st Store) {
		o := mkOrg(t, st, "o1", "o1@x.test", nil)
		o.Settings = domain.OrgSettings{Email: &domain.EmailSettings{FromAddress: "a@b.test",
			SMTP: domain.SMTPSettings{Host: "smtp", Password: "smtp-pw"}, IMAP: &domain.IMAPSettings{Host: "imap", Password: "imap-pw"}},
			NotifyEmails: []string{"t@b.test"}}
		o.InboundToken = "tok"
		if err := st.UpdateOrg(ctx, o); err != nil {
			t.Fatal(err)
		}
		got, err := st.GetOrg(ctx, "o1")
		if err != nil || got.Settings.Email.SMTP.Password != "smtp-pw" || got.Settings.Email.IMAP.Password != "imap-pw" || got.InboundToken != "tok" || got.Settings.NotifyEmails[0] != "t@b.test" {
			t.Fatalf("settings did not round-trip: %v %+v", err, got.Settings)
		}
		if in, _ := st.ListInboxOrgs(ctx); len(in) != 1 || in[0].ID != "o1" {
			t.Errorf("ListInboxOrgs = %v", in)
		}

		// The poller owns the cursor: settings updates must never rewind it.
		if err := st.SetInboxCursor(ctx, "o1", domain.InboxCursor{UIDValidity: 9, LastUID: 120}); err != nil {
			t.Fatal(err)
		}
		got.Name = "renamed"
		got.InboxCursor = domain.InboxCursor{} // a stale copy, as a concurrent settings save would hold
		if err := st.UpdateOrg(ctx, got); err != nil {
			t.Fatal(err)
		}
		after, _ := st.GetOrg(ctx, "o1")
		if after.Name != "renamed" || after.InboxCursor != (domain.InboxCursor{UIDValidity: 9, LastUID: 120}) {
			t.Fatalf("after update: %+v", after)
		}
		if err := st.SetInboxCursor(ctx, "ghost", domain.InboxCursor{}); !errors.Is(err, ErrNotFound) {
			t.Errorf("cursor for a missing org: %v", err)
		}
		// Removing IMAP takes the org out of the polling list.
		after.Settings.Email.IMAP = nil
		_ = st.UpdateOrg(ctx, after)
		if in, _ := st.ListInboxOrgs(ctx); len(in) != 0 {
			t.Errorf("ListInboxOrgs after removing IMAP = %d", len(in))
		}
		// Mutating a returned org must not change what is stored.
		g1, _ := st.GetOrg(ctx, "o1")
		g1.Settings.Email.SMTP.Host = "tampered"
		if g2, _ := st.GetOrg(ctx, "o1"); g2.Settings.Email.SMTP.Host != "smtp" {
			t.Error("stored settings share memory with returned copies")
		}
	})
}

func TestTenantIsolation(t *testing.T) {
	each(t, func(t *testing.T, st Store) {
		mkOrg(t, st, "a", "a@x.test", nil)
		mkOrg(t, st, "b", "b@x.test", nil)
		ag := domain.Agent{ID: "ag1", OrgID: "a", Name: "A's agent", Status: domain.AgentActive}
		if err := st.PutAgent(ctx, ag); err != nil {
			t.Fatal(err)
		}
		pr := domain.Prospect{ID: "p1", OrgID: "a", AgentID: "ag1", Name: "P", Stage: domain.StageNew, Contact: domain.Contact{Email: "P@X.test", Phone: "+961 3 123 456"}}
		_ = st.PutProspect(ctx, pr)
		_ = st.PutMessage(ctx, domain.Message{ID: "m1", OrgID: "a", ProspectID: "p1", Status: domain.MsgPendingApproval})

		if _, err := st.GetAgent(ctx, "b", "ag1"); !errors.Is(err, ErrNotFound) {
			t.Errorf("org b read org a's agent: %v", err)
		}
		if _, err := st.GetProspect(ctx, "b", "p1"); !errors.Is(err, ErrNotFound) {
			t.Errorf("org b read org a's prospect: %v", err)
		}
		if _, err := st.GetMessage(ctx, "b", "m1"); !errors.Is(err, ErrNotFound) {
			t.Errorf("org b read org a's message: %v", err)
		}
		// Another org writing to an existing ID must fail and change nothing.
		hijack := ag
		hijack.OrgID, hijack.Name = "b", "hijacked"
		if err := st.PutAgent(ctx, hijack); !errors.Is(err, ErrNotFound) {
			t.Errorf("cross-tenant overwrite: %v", err)
		}
		if got, _ := st.GetAgent(ctx, "a", "ag1"); got.Name != "A's agent" {
			t.Errorf("agent was overwritten: %+v", got)
		}
		if l, _ := st.ListProspects(ctx, "b", ProspectFilter{}); len(l) != 0 {
			t.Error("org b lists org a's prospects")
		}
		// Contact matching is per org, case-insensitive on email and suffix-based on phone.
		for _, c := range []struct{ email, phone string }{{"p@x.test", ""}, {"", "03123456"}, {"", "+961-3-123-456"}, {"", "00961 3 123 456"}} {
			if got, err := st.FindProspectByContact(ctx, "a", c.email, c.phone); err != nil || got.ID != "p1" {
				t.Errorf("FindProspectByContact(%q,%q): %v", c.email, c.phone, err)
			}
			if _, err := st.FindProspectByContact(ctx, "b", c.email, c.phone); !errors.Is(err, ErrNotFound) {
				t.Errorf("FindProspectByContact leaked across orgs (%q,%q)", c.email, c.phone)
			}
		}
		for _, c := range []struct{ email, phone string }{{"", ""}, {"other@x.test", ""}, {"", "12345"}, {"", "999999999"}} {
			if _, err := st.FindProspectByContact(ctx, "a", c.email, c.phone); !errors.Is(err, ErrNotFound) {
				t.Errorf("FindProspectByContact(%q,%q) should find nothing", c.email, c.phone)
			}
		}
		// Two prospects that share the last digits: the exact number wins.
		_ = st.PutProspect(ctx, domain.Prospect{ID: "p2", OrgID: "a", AgentID: "ag1", Name: "Other", Stage: domain.StageNew, UpdatedAt: time.Now().Add(time.Hour), Contact: domain.Contact{Phone: "+20 3 123 456"}})
		if got, err := st.FindProspectByContact(ctx, "a", "", "20 3 123 456"); err != nil || got.ID != "p2" {
			t.Errorf("exact phone should beat a suffix match: %v %v", got.ID, err)
		}
		if act, _ := st.ListActiveAgents(ctx); len(act) != 1 {
			t.Errorf("active agents = %d", len(act))
		}
	})
}

func TestCountsAndSendWindow(t *testing.T) {
	each(t, func(t *testing.T, st Store) {
		mkOrg(t, st, "o", "o@x.test", nil)
		now := time.Now()
		meeting := now.Add(time.Hour)
		for _, p := range []domain.Prospect{
			{ID: "1", Stage: domain.StageResearched, Research: &domain.Research{}},
			{ID: "2", Stage: domain.StageQualified, DealValue: 1000},
			{ID: "3", Stage: domain.StageMeetingBooked, DealValue: 500, MeetingAt: &meeting},
			{ID: "4", Stage: domain.StageWon, DealValue: 2000, MeetingAt: &meeting},
			{ID: "5", Stage: domain.StageLost, DealValue: 9999},
		} {
			p.OrgID, p.AgentID, p.Name = "o", "ag", p.ID
			_ = st.PutProspect(ctx, p)
		}
		c, err := st.Counts(ctx, "o")
		if err != nil {
			t.Fatal(err)
		}
		if c.Researched != 1 || c.Meetings != 2 || c.Pipeline != 1500 || c.WonValue != 2000 {
			t.Fatalf("counts = %+v", c)
		}
		s := BuildStats(c)
		if s.ProspectsIdentified != 5 || s.QualifiedLeads != 3 || s.Won != 1 || s.MeetingsBooked != 2 || s.EstimatedPipeline != 1500 {
			t.Fatalf("stats = %+v", s)
		}

		old, recent := now.Add(-30*time.Hour), now.Add(-time.Hour)
		put := func(id, agent string, at *time.Time, st2 domain.MessageStatus, dir domain.Direction) {
			_ = st.PutMessage(ctx, domain.Message{ID: id, OrgID: "o", AgentID: agent, ProspectID: "1", Direction: dir, Status: st2, SentAt: at})
		}
		put("m1", "ag", &old, domain.MsgSent, domain.Outbound)
		put("m2", "ag", &recent, domain.MsgSent, domain.Outbound)
		put("m3", "other", &recent, domain.MsgSent, domain.Outbound)
		put("m4", "ag", nil, domain.MsgPendingApproval, domain.Outbound)
		put("m5", "ag", &recent, domain.MsgReceived, domain.Inbound)
		since := now.Add(-24 * time.Hour)
		if n, _ := st.CountSent(ctx, "o", "ag", since); n != 1 {
			t.Errorf("sent by ag in 24h = %d, want 1", n)
		}
		if n, _ := st.CountSent(ctx, "o", "", since); n != 2 {
			t.Errorf("sent by anyone in 24h = %d, want 2", n)
		}
		if n, _ := st.CountSent(ctx, "someone-else", "", since); n != 0 {
			t.Errorf("other org's count = %d", n)
		}
		if s := BuildStats(func() Counts { c, _ := st.Counts(ctx, "o"); return c }()); s.OutreachSent != 3 || s.AwaitingApproval != 1 {
			t.Errorf("message stats = %+v", s)
		}
	})
}

func TestAPIKeys(t *testing.T) {
	each(t, func(t *testing.T, st Store) {
		mkOrg(t, st, "a", "a@x.test", nil)
		mkOrg(t, st, "b", "b@x.test", nil)
		if err := st.PutAPIKey(ctx, domain.APIKey{ID: "k1", OrgID: "a", Name: "ci", Prefix: "aisp_abc", Hash: "h1"}); err != nil {
			t.Fatal(err)
		}
		if k, err := st.APIKeyByHash(ctx, "h1"); err != nil || k.OrgID != "a" {
			t.Errorf("APIKeyByHash: %v", err)
		}
		if _, err := st.APIKeyByHash(ctx, "nope"); !errors.Is(err, ErrNotFound) {
			t.Error("unknown hash")
		}
		if ks, _ := st.ListAPIKeys(ctx, "b"); len(ks) != 0 {
			t.Error("org b sees org a's keys")
		}
		if err := st.DeleteAPIKey(ctx, "b", "k1"); !errors.Is(err, ErrNotFound) {
			t.Errorf("org b deleted org a's key: %v", err)
		}
		if err := st.DeleteAPIKey(ctx, "a", "k1"); err != nil {
			t.Fatal(err)
		}
		if _, err := st.APIKeyByHash(ctx, "h1"); !errors.Is(err, ErrNotFound) {
			t.Error("deleted key still resolves")
		}
	})
}

// Credentials must be unreadable in the database itself, and a wrong key must
// fail loudly rather than return garbage.
func TestPostgresEncryptsCredentialsAtRest(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run against Postgres")
	}
	st := backends(t)["postgres"](t).(*Postgres)
	o := mkOrg(t, st, "enc", "enc@x.test", nil)
	o.Settings.Email = &domain.EmailSettings{FromAddress: "a@b.test", SMTP: domain.SMTPSettings{Host: "smtp", Password: "super-secret-smtp-password"}}
	o.Settings.GooglePlacesKey = "AIza-super-secret-key"
	if err := st.UpdateOrg(ctx, o); err != nil {
		t.Fatal(err)
	}
	var enc, data string
	if err := st.pool.QueryRow(ctx, `SELECT settings_enc, data::text FROM orgs WHERE id='enc'`).Scan(&enc, &data); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"super-secret-smtp-password", "AIza-super-secret-key"} {
		if strings.Contains(enc, secret) || strings.Contains(data, secret) {
			t.Fatalf("plaintext credential %q is stored in the database", secret)
		}
	}
	if !strings.HasPrefix(enc, "v1:") {
		t.Errorf("settings_enc = %q", enc)
	}
	// Another key cannot read it.
	wrong, err := NewPostgres(ctx, url, secrets.FromPassphrase("a-different-key"))
	if err != nil {
		t.Fatal(err)
	}
	defer wrong.Close()
	if _, err := wrong.GetOrg(ctx, "enc"); err == nil || !strings.Contains(err.Error(), "decrypt failed") {
		t.Fatalf("wrong SECRETS_KEY must fail to decrypt, got %v", err)
	}
	// Two encryptions of the same data differ (random nonce).
	a1, _ := st.orgArgs(o)
	a2, _ := st.orgArgs(o)
	if a1[8] == a2[8] {
		t.Error("ciphertext must not be deterministic")
	}
}
