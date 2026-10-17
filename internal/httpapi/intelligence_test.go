package httpapi_test

import (
	"strings"
	"testing"

	"github.com/hussein/ai-salesperson/internal/domain"
)

func TestCompanyIntelligenceEndToEnd(t *testing.T) {
	e := newEnv(t)
	tok := e.signup("Acme", "a@acme.test")
	e.setPlan(e.orgID(tok), domain.PlanGrowth)

	started := e.ok(202, "POST", "/v1/intelligence/analyze", tok, map[string]any{"company_name": "Rival Restaurants", "website": "https://rival.test"})
	rep := started["report"].(map[string]any)
	job := started["job"].(map[string]any)
	if rep["status"] != "running" || rep["company_name"] != "Rival Restaurants" {
		t.Fatalf("started report = %v", rep)
	}
	reportID := str(rep, "id")

	e.waitJob(tok, str(job, "id"))

	done := e.ok(200, "GET", "/v1/intelligence/reports/"+reportID, tok, nil)
	if done["status"] != "done" {
		t.Fatalf("final status = %v", done)
	}
	profile := done["profile"].(map[string]any)
	if profile["summary"] == "" || profile["summary"] == nil {
		t.Fatalf("profile = %v", profile)
	}
	competitors := done["competitors"].([]any)
	gaps := done["gaps"].([]any)
	recs := done["recommendations"].([]any)
	sources := done["sources"].([]any)
	if len(competitors) == 0 || len(gaps) == 0 || len(recs) == 0 || len(sources) == 0 {
		t.Fatalf("report incomplete: competitors=%d gaps=%d recs=%d sources=%d", len(competitors), len(gaps), len(recs), len(sources))
	}
	// A gap carries the fields the frontend renders.
	g := gaps[0].(map[string]any)
	if g["area"] == "" || g["severity"] == "" {
		t.Fatalf("gap = %v", g)
	}

	// It shows up in the list, newest first alongside a second report.
	started2 := e.ok(202, "POST", "/v1/intelligence/analyze", tok, map[string]any{"company_name": "Second Co"})
	e.waitJob(tok, str(started2["job"].(map[string]any), "id"))
	list := e.list("/v1/intelligence/reports", tok)
	if len(list) != 2 || list[0]["company_name"] != "Second Co" {
		t.Fatalf("list = %v", list)
	}

	// Delete.
	e.ok(204, "DELETE", "/v1/intelligence/reports/"+reportID, tok, nil)
	e.ok(404, "GET", "/v1/intelligence/reports/"+reportID, tok, nil)
}

func TestCompanyIntelligenceValidation(t *testing.T) {
	e := newEnv(t)
	tok := e.signup("Acme", "a@acme.test")
	e.setPlan(e.orgID(tok), domain.PlanGrowth)

	e.ok(400, "POST", "/v1/intelligence/analyze", tok, map[string]any{"company_name": ""})
	e.ok(400, "POST", "/v1/intelligence/analyze", tok, map[string]any{"company_name": "Acme", "website": "not a url"})
	e.ok(400, "POST", "/v1/intelligence/analyze", tok, map[string]any{"company_name": strings.Repeat("x", 300)})
}

func TestCompanyIntelligencePlanGatingAndQuota(t *testing.T) {
	e := newEnv(t)
	tok := e.signup("Acme", "a@acme.test")
	oid := e.orgID(tok)

	// Trial: a limited taste, then blocked. (A fresh signup is already on
	// PlanTrial with a valid TrialEndsAt; test this before touching the plan
	// with setPlan, which always clears TrialEndsAt.)
	for i := 0; i < 3; i++ {
		started := e.ok(202, "POST", "/v1/intelligence/analyze", tok, map[string]any{"company_name": "Acme"})
		e.waitJob(tok, str(started["job"].(map[string]any), "id"))
	}
	code, out := e.do("POST", "/v1/intelligence/analyze", tok, map[string]any{"company_name": "Acme"})
	if code != 403 || !strings.Contains(str(out, "error"), "30 days") {
		t.Fatalf("over quota = %d %v", code, out)
	}

	// Starter: no Company Intelligence at all.
	e.setPlan(oid, domain.PlanStarter)
	code, out = e.do("POST", "/v1/intelligence/analyze", tok, map[string]any{"company_name": "Acme"})
	if code != 403 || !strings.Contains(str(out, "error"), "Growth") {
		t.Fatalf("starter = %d %v", code, out)
	}
}

func TestCompanyIntelligenceTenantIsolation(t *testing.T) {
	e := newEnv(t)
	tok := e.signup("Acme", "a@acme.test")
	e.setPlan(e.orgID(tok), domain.PlanGrowth)
	other := e.signup("Rival", "r@rival.test")
	e.setPlan(e.orgID(other), domain.PlanGrowth)

	started := e.ok(202, "POST", "/v1/intelligence/analyze", tok, map[string]any{"company_name": "Acme"})
	rid := str(started["report"].(map[string]any), "id")
	e.waitJob(tok, str(started["job"].(map[string]any), "id"))

	e.ok(404, "GET", "/v1/intelligence/reports/"+rid, other, nil)
	e.ok(404, "DELETE", "/v1/intelligence/reports/"+rid, other, nil)
	if list := e.list("/v1/intelligence/reports", other); len(list) != 0 {
		t.Fatalf("other org sees reports: %v", list)
	}
	// It's still there for its real owner.
	e.ok(200, "GET", "/v1/intelligence/reports/"+rid, tok, nil)
}
