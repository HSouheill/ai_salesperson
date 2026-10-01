package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hussein/ai-salesperson/internal/ai"
	"github.com/hussein/ai-salesperson/internal/billing"
	"github.com/hussein/ai-salesperson/internal/channels"
	"github.com/hussein/ai-salesperson/internal/config"
	"github.com/hussein/ai-salesperson/internal/domain"
	"github.com/hussein/ai-salesperson/internal/httpapi"
	"github.com/hussein/ai-salesperson/internal/inbound"
	"github.com/hussein/ai-salesperson/internal/integrations"
	"github.com/hussein/ai-salesperson/internal/jobs"
	"github.com/hussein/ai-salesperson/internal/queue"
	"github.com/hussein/ai-salesperson/internal/ratelimit"
	"github.com/hussein/ai-salesperson/internal/sales"
	"github.com/hussein/ai-salesperson/internal/secrets"
	"github.com/hussein/ai-salesperson/internal/sources"
	"github.com/hussein/ai-salesperson/internal/store"
	"github.com/hussein/ai-salesperson/internal/webfetch"
)

type env struct {
	t     *testing.T
	srv   *httptest.Server
	ch    *fakeChannels
	st    store.Store
	svc   *sales.Service
	clock *fakeClock
	q     *queue.Queue
	run   *jobs.Runner
}

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// fakeChannels records what would have been delivered.
type fakeChannels struct {
	mu           sync.Mutex
	sent         []channels.Outgoing
	unconfigured map[string]bool
	sendErr      error // when set, every Send fails with it
}

func (f *fakeChannels) For(_ domain.Org, name string) (channels.Channel, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.unconfigured[name] {
		return nil, fmt.Errorf("%w: connect %s in Settings", channels.ErrNotConfigured, name)
	}
	return fakeChan{f: f, name: name}, nil
}

func (f *fakeChannels) Sent() []channels.Outgoing {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]channels.Outgoing(nil), f.sent...)
}

type fakeChan struct {
	f    *fakeChannels
	name string
}

func (c fakeChan) Name() string { return c.name }
func (c fakeChan) Recipient(email, phone string) string {
	if c.name == "whatsapp" {
		return phone
	}
	return email
}
func (c fakeChan) Send(_ context.Context, o channels.Outgoing) error {
	c.f.mu.Lock()
	defer c.f.mu.Unlock()
	if c.f.sendErr != nil {
		return c.f.sendErr
	}
	c.f.sent = append(c.f.sent, o)
	return nil
}

type opts struct {
	allowPrivate bool
	overpass     string
	places       string
	crm          integrations.Pusher
	stripe       *billing.Stripe
	cfg          func(*config.Config)
	ai           ai.Provider
}

func newEnv(t *testing.T) *env { return newEnvWith(t, opts{}) }

func newEnvWith(t *testing.T, o opts) *env {
	t.Helper()
	var st store.Store = store.NewMemory()
	if url := os.Getenv("TEST_DATABASE_URL"); url != "" { // run the suite against real Postgres
		pg, err := store.NewPostgres(context.Background(), url, secrets.FromPassphrase("test"))
		if err != nil {
			t.Fatal(err)
		}
		st = pg
		t.Cleanup(pg.Close)
		// Each test starts from an empty database, like the in-memory store.
		pool, err := pgxpool.New(context.Background(), url)
		if err != nil {
			t.Fatal(err)
		}
		defer pool.Close()
		if _, err := pool.Exec(context.Background(), `TRUNCATE api_keys, messages, prospects, agents, users, orgs CASCADE`); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Config{JWTSecret: "test-secret", PublicURL: "http://api.test", WebURL: "http://web.test", AllowPrivateNet: o.allowPrivate}
	if o.cfg != nil {
		o.cfg(&cfg)
	}
	fc := &fakeChannels{unconfigured: map[string]bool{}}
	clock := &fakeClock{t: time.Now()}
	var provider ai.Provider = ai.Mock{}
	if o.ai != nil {
		provider = o.ai
	}
	svc := sales.New(st, provider, fc, webfetch.New(o.allowPrivate),
		sources.Factory{OverpassURL: o.overpass, PlacesURL: o.places}, o.crm, cfg.PublicURL)
	svc.Now = clock.Now
	ctx, cancel := context.WithCancel(context.Background())
	q := queue.NewMemory(64)
	poller := &inbound.Poller{Store: st, Handler: svc, AllowPrivate: true}
	runner := &jobs.Runner{Q: q, Sales: svc, Store: st, Poller: poller}
	runner.Register()
	q.Start(ctx, 2)
	stripe := o.stripe
	if stripe == nil {
		stripe = &billing.Stripe{}
	}
	srv := httptest.NewServer(httpapi.New(cfg, st, svc, runner, q, poller, stripe, ratelimit.NewMemory()))
	t.Cleanup(func() { srv.Close(); cancel(); q.Close() })
	return &env{t: t, srv: srv, ch: fc, st: st, svc: svc, clock: clock, q: q, run: runner}
}

// do sends a request and returns the status and decoded JSON body.
func (e *env) do(method, path, token string, body any) (int, map[string]any) {
	e.t.Helper()
	var rd bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = *bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, &rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func (e *env) ok(want int, method, path, token string, body any) map[string]any {
	e.t.Helper()
	code, out := e.do(method, path, token, body)
	if code != want {
		e.t.Fatalf("%s %s: status %d, want %d; body=%v", method, path, code, want, out)
	}
	return out
}

func (e *env) signup(org, email string) string {
	out := e.ok(201, "POST", "/v1/auth/signup", "", map[string]string{"org_name": org, "email": email, "password": "correct-horse"})
	return out["token"].(string)
}

func str(m map[string]any, k string) string { s, _ := m[k].(string); return s }

func TestSalesFlowEndToEnd(t *testing.T) {
	e := newEnv(t)
	tok := e.signup("Acme Software", "owner@acme.test")

	// Agent + brain. The starter plan allows one agent.
	ag := e.ok(201, "POST", "/v1/agents", tok, map[string]any{
		"name":      "Restaurant Hunter",
		"profile":   map[string]any{"name": "Acme", "description": "We build ordering apps for restaurants"},
		"targeting": map[string]any{"industries": []string{"restaurants"}, "locations": []string{"Lebanon", "UAE"}},
	})
	agentID := str(ag, "id")
	e.ok(403, "POST", "/v1/agents", tok, map[string]any{"name": "Second"})
	e.ok(409, "POST", "/v1/agents/"+agentID+"/status", tok, map[string]any{"status": "active"}) // no brain yet
	brain := e.ok(200, "POST", "/v1/agents/"+agentID+"/brain", tok, nil)
	if brain["brain"] == nil {
		t.Fatal("brain not generated")
	}
	e.ok(200, "POST", "/v1/agents/"+agentID+"/status", tok, map[string]any{"status": "active"})

	// Import (duplicate email skipped, prospect without any contact is allowed but can't be messaged).
	imp := e.ok(201, "POST", "/v1/agents/"+agentID+"/prospects", tok, map[string]any{"prospects": []map[string]any{
		{"name": "ABC Restaurant", "contact_name": "Ahmed", "contact": map[string]string{"email": "ahmed@abc.test"}, "notes": "four locations, no online ordering"},
		{"name": "ABC Restaurant again", "contact": map[string]string{"email": "AHMED@abc.test"}},
		{"name": "No Contact Cafe"},
	}})
	if imp["added"] != float64(2) || imp["skipped"] != float64(1) {
		t.Fatalf("import result = %v", imp)
	}

	// Bulk research runs as a background job.
	job := e.ok(202, "POST", "/v1/agents/"+agentID+"/research", tok, nil)
	jobID := str(job, "id")
	deadline := time.Now().Add(5 * time.Second)
	for {
		j := e.ok(200, "GET", "/v1/jobs/"+jobID, tok, nil)
		if j["status"] == "done" {
			if j["done"] != float64(2) || j["failed"] != float64(0) {
				t.Fatalf("job = %v", j)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("job did not finish: %v", j)
		}
		time.Sleep(20 * time.Millisecond)
	}

	code, list := e.do("GET", "/v1/agents/"+agentID+"/prospects", tok, nil)
	_ = list
	if code != 200 {
		t.Fatal(code)
	}
	prospects := e.list("/v1/agents/"+agentID+"/prospects", tok)
	var abc map[string]any
	for _, p := range prospects {
		if p["name"] == "ABC Restaurant" {
			abc = p
		}
	}
	if abc == nil || abc["score"] != float64(72) || abc["stage"] != "researched" {
		t.Fatalf("abc = %v", abc)
	}
	pid := str(abc, "id")

	// Outreach waits for human approval, then sends.
	msg := e.ok(201, "POST", "/v1/prospects/"+pid+"/outreach", tok, nil)
	if msg["status"] != "pending_approval" {
		t.Fatalf("draft status = %v", msg["status"])
	}
	if got := e.ok(200, "GET", "/v1/dashboard", tok, nil); got["awaiting_approval"] != float64(1) {
		t.Fatalf("dashboard = %v", got)
	}
	sent := e.ok(200, "POST", "/v1/messages/"+str(msg, "id")+"/review", tok, map[string]any{"approve": true})
	if sent["status"] != "sent" {
		t.Fatalf("review = %v", sent)
	}
	e.ok(409, "POST", "/v1/messages/"+str(msg, "id")+"/review", tok, map[string]any{"approve": true}) // no double send

	// Reply: a pricing question keeps the conversation going with a drafted answer.
	r := e.ok(200, "POST", "/v1/prospects/"+pid+"/replies", tok, map[string]any{"body": "How much does it cost?"})
	if r["intent"] != "question" || r["response"] == nil {
		t.Fatalf("reply = %v", r)
	}
	if st := r["prospect"].(map[string]any)["stage"]; st != "engaged" {
		t.Fatalf("stage = %v", st)
	}
	// Objection is recognised, not ignored.
	if r := e.ok(200, "POST", "/v1/prospects/"+pid+"/replies", tok, map[string]any{"body": "We already have a POS system."}); r["intent"] != "objection" {
		t.Fatalf("objection reply = %v", r)
	}
	// Meeting request qualifies the lead; booking hands it to the sales team.
	r = e.ok(200, "POST", "/v1/prospects/"+pid+"/replies", tok, map[string]any{"body": "Sounds good, can we set up a call?"})
	if st := r["prospect"].(map[string]any)["stage"]; st != "qualified" {
		t.Fatalf("stage = %v", st)
	}
	e.ok(400, "POST", "/v1/prospects/"+pid+"/meeting", tok, map[string]any{"at": time.Now().Add(-time.Hour)})
	e.ok(200, "POST", "/v1/prospects/"+pid+"/meeting", tok, map[string]any{"at": time.Now().Add(48 * time.Hour)})

	d := e.ok(200, "GET", "/v1/dashboard", tok, nil)
	if d["prospects_identified"] != float64(2) || d["prospects_researched"] != float64(2) ||
		d["meetings_booked"] != float64(1) || d["qualified_leads"] != float64(1) || d["outreach_sent"] != float64(1) {
		t.Fatalf("dashboard = %v", d)
	}

	// Tenant isolation: another organization cannot see or touch any of it.
	other := e.signup("Rival Co", "owner@rival.test")
	e.ok(404, "GET", "/v1/prospects/"+pid, other, nil)
	e.ok(404, "GET", "/v1/agents/"+agentID, other, nil)
	e.ok(404, "POST", "/v1/prospects/"+pid+"/replies", other, map[string]any{"body": "hi"})
	e.ok(404, "GET", "/v1/jobs/"+jobID, other, nil)
	if got := e.ok(200, "GET", "/v1/dashboard", other, nil); got["prospects_identified"] != float64(0) {
		t.Fatalf("other dashboard = %v", got)
	}
}

func (e *env) list(path, tok string) []map[string]any {
	e.t.Helper()
	req, _ := http.NewRequest("GET", e.srv.URL+path, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		e.t.Fatal(err)
	}
	return out
}

func TestUnsubscribeStopsAllContact(t *testing.T) {
	e := newEnv(t)
	tok := e.signup("Acme", "a@acme.test")
	ag := e.ok(201, "POST", "/v1/agents", tok, map[string]any{"name": "A", "profile": map[string]any{"name": "Acme", "description": "d"}})
	aid := str(ag, "id")
	e.ok(200, "POST", "/v1/agents/"+aid+"/brain", tok, nil)
	e.ok(201, "POST", "/v1/agents/"+aid+"/prospects", tok, map[string]any{"prospects": []map[string]any{
		{"name": "P", "contact": map[string]string{"email": "p@p.test"}}}})
	pid := str(e.list("/v1/agents/"+aid+"/prospects", tok)[0], "id")

	draft := e.ok(201, "POST", "/v1/prospects/"+pid+"/outreach", tok, nil)
	r := e.ok(200, "POST", "/v1/prospects/"+pid+"/replies", tok, map[string]any{"body": "Please unsubscribe me"})
	if r["intent"] != "unsubscribe" || r["response"] != nil {
		t.Fatalf("reply = %v", r)
	}
	// The pending draft was cancelled and can no longer be approved.
	e.ok(409, "POST", "/v1/messages/"+str(draft, "id")+"/review", tok, map[string]any{"approve": true})
	e.ok(409, "POST", "/v1/prospects/"+pid+"/outreach", tok, nil)
	if p := e.ok(200, "GET", "/v1/prospects/"+pid, tok, nil)["prospect"].(map[string]any); p["stage"] != "do_not_contact" {
		t.Fatalf("stage = %v", p["stage"])
	}
}

func TestAuth(t *testing.T) {
	e := newEnv(t)
	e.ok(401, "GET", "/v1/agents", "", nil)
	e.ok(401, "GET", "/v1/agents", "not.a.token", nil)
	e.signup("Acme", "a@acme.test")
	e.ok(409, "POST", "/v1/auth/signup", "", map[string]string{"org_name": "x", "email": "A@ACME.test", "password": "correct-horse"})
	e.ok(400, "POST", "/v1/auth/signup", "", map[string]string{"org_name": "x", "email": "b@b.test", "password": "short"})
	e.ok(401, "POST", "/v1/auth/login", "", map[string]string{"email": "a@acme.test", "password": "wrong-password"})
	e.ok(401, "POST", "/v1/auth/login", "", map[string]string{"email": "nobody@acme.test", "password": "correct-horse"})
	tok := e.ok(200, "POST", "/v1/auth/login", "", map[string]string{"email": "a@acme.test", "password": "correct-horse"})["token"].(string)
	e.ok(200, "GET", "/v1/me", tok, nil)
	// A token signed with another secret must be rejected.
	if !strings.Contains(tok, ".") {
		t.Fatal("token format")
	}
	forged := tok[:strings.LastIndex(tok, ".")+1] + "AAAA"
	e.ok(401, "GET", "/v1/me", forged, nil)
}

func TestWebsiteAnalysisRejectsInternalAddresses(t *testing.T) {
	e := newEnv(t)
	tok := e.signup("Acme", "a@acme.test")
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("secret")) }))
	defer local.Close()
	for _, u := range []string{local.URL, "http://169.254.169.254/latest/meta-data", "file:///etc/passwd", "ftp://x.test"} {
		if code, _ := e.do("POST", "/v1/onboarding/analyze", tok, map[string]string{"website": u}); code != 400 {
			t.Errorf("analyze %s: status %d, want 400", u, code)
		}
	}
}
