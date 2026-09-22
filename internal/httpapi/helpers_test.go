package httpapi_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/hussein/ai-salesperson/internal/ai"
	"github.com/hussein/ai-salesperson/internal/domain"
	"github.com/hussein/ai-salesperson/internal/integrations"
)

// capturingAI wraps the mock and records every request; it can also fail on demand.
type capturingAI struct {
	mu      sync.Mutex
	reqs    []ai.Request
	failFor int // fail this many upcoming calls
}

func (c *capturingAI) Complete(ctx context.Context, r ai.Request) (string, error) {
	c.mu.Lock()
	c.reqs = append(c.reqs, r)
	if c.failFor > 0 {
		c.failFor--
		c.mu.Unlock()
		return "", errors.New("provider down")
	}
	c.mu.Unlock()
	return ai.Mock{}.Complete(ctx, r)
}

func (c *capturingAI) Last(task ai.Task) ai.Request {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := len(c.reqs) - 1; i >= 0; i-- {
		if c.reqs[i].Task == task {
			return c.reqs[i]
		}
	}
	return ai.Request{}
}

func (c *capturingAI) Count(task ai.Task) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, r := range c.reqs {
		if r.Task == task {
			n++
		}
	}
	return n
}

type crmEvent struct {
	Event    integrations.Event
	Prospect string
}

type fakeCRM struct {
	mu     sync.Mutex
	events []crmEvent
}

func (f *fakeCRM) Push(_ context.Context, _ domain.Org, _ domain.Agent, p domain.Prospect, ev integrations.Event) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, crmEvent{ev, p.Name})
	return "crm-1", nil
}

func (f *fakeCRM) Events() []crmEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]crmEvent(nil), f.events...)
}

func (e *env) orgID(tok string) string {
	e.t.Helper()
	return e.ok(200, "GET", "/v1/me", tok, nil)["org"].(map[string]any)["id"].(string)
}

// setPlan changes an org's plan directly (billing is tested separately).
func (e *env) setPlan(orgID string, plan domain.Plan) {
	e.t.Helper()
	o, err := e.st.GetOrg(context.Background(), orgID)
	if err != nil {
		e.t.Fatal(err)
	}
	o.Plan, o.TrialEndsAt = plan, nil
	if err := e.st.UpdateOrg(context.Background(), o); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) setSettings(orgID string, f func(*domain.OrgSettings)) {
	e.t.Helper()
	o, err := e.st.GetOrg(context.Background(), orgID)
	if err != nil {
		e.t.Fatal(err)
	}
	f(&o.Settings)
	if err := e.st.UpdateOrg(context.Background(), o); err != nil {
		e.t.Fatal(err)
	}
}

// agent creates an agent with a generated brain (and launches it).
func (e *env) agent(tok string, extra map[string]any) string {
	e.t.Helper()
	body := map[string]any{"name": "Hunter", "profile": map[string]any{"name": "Acme", "description": "We build ordering apps"},
		"targeting": map[string]any{"industries": []string{"restaurants"}, "locations": []string{"Lebanon"}}}
	for k, v := range extra {
		body[k] = v
	}
	id := str(e.ok(201, "POST", "/v1/agents", tok, body), "id")
	e.ok(200, "POST", "/v1/agents/"+id+"/brain", tok, nil)
	e.ok(200, "POST", "/v1/agents/"+id+"/status", tok, map[string]any{"status": "active"})
	return id
}

func (e *env) prospect(tok, agentID, name, email string) string {
	e.t.Helper()
	e.ok(201, "POST", "/v1/agents/"+agentID+"/prospects", tok, map[string]any{"prospects": []map[string]any{
		{"name": name, "contact_name": "Ahmed", "contact": map[string]string{"email": email}, "notes": "four locations"}}})
	for _, p := range e.list("/v1/agents/"+agentID+"/prospects", tok) {
		if p["name"] == name {
			return str(p, "id")
		}
	}
	e.t.Fatalf("prospect %s not found", name)
	return ""
}

func (e *env) stageOf(tok, pid string) string {
	e.t.Helper()
	return str(e.ok(200, "GET", "/v1/prospects/"+pid, tok, nil)["prospect"].(map[string]any), "stage")
}

// waitJob polls a job until it is done.
func (e *env) waitJob(tok, id string) map[string]any {
	e.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		j := e.ok(200, "GET", "/v1/jobs/"+id, tok, nil)
		if j["status"] == "done" {
			return j
		}
		time.Sleep(20 * time.Millisecond)
	}
	e.t.Fatal("job did not finish")
	return nil
}

func (e *env) raw(method, path, contentType, body string, headers map[string]string) (int, string) {
	e.t.Helper()
	req, _ := http.NewRequest(method, e.srv.URL+path, strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return resp.StatusCode, sb.String()
}
