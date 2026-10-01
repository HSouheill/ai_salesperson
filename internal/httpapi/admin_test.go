package httpapi_test

import (
	"strings"
	"testing"

	"github.com/hussein/ai-salesperson/internal/config"
	"github.com/hussein/ai-salesperson/internal/domain"
)

func adminEnv(t *testing.T, token string) *env {
	return newEnvWith(t, opts{cfg: func(c *config.Config) { c.AdminToken = token }})
}

func TestAdminAPIClosedByDefault(t *testing.T) {
	e := newEnv(t) // no ADMIN_TOKEN configured
	e.ok(503, "GET", "/v1/admin/orgs", "", nil)
	e.ok(503, "GET", "/v1/admin/health", "", nil)
}

func TestAdminAuth(t *testing.T) {
	e := adminEnv(t, "s3cret-admin-token")
	e.ok(401, "GET", "/v1/admin/orgs", "", nil)            // no token at all
	e.ok(401, "GET", "/v1/admin/orgs", "wrong-token", nil) // wrong token
	// A normal customer login must not work as the admin token.
	tok := e.signup("Acme", "a@acme.test")
	e.ok(401, "GET", "/v1/admin/orgs", tok, nil)
	e.ok(200, "GET", "/v1/admin/orgs", "s3cret-admin-token", nil)
}

func TestAdminListAndSuspendOrgs(t *testing.T) {
	e := adminEnv(t, "admintok")
	tok1 := e.signup("Acme Software", "owner@acme.test")
	e.signup("Cedar Restaurants", "owner@cedar.test")
	acmeID := e.orgID(tok1)

	listed := e.ok(200, "GET", "/v1/admin/orgs", "admintok", nil)
	orgs := listed["orgs"].([]any)
	if len(orgs) != 2 {
		t.Fatalf("orgs = %d", len(orgs))
	}

	found := e.ok(200, "GET", "/v1/admin/orgs?q=owner@acme.test", "admintok", nil)["orgs"].([]any)
	if len(found) != 1 || found[0].(map[string]any)["id"] != acmeID {
		t.Fatalf("search = %v", found)
	}

	// Suspend: the org immediately loses entitlement, independent of its trial/plan.
	s := e.ok(200, "POST", "/v1/admin/orgs/"+acmeID+"/suspend", "admintok", map[string]any{"reason": "payment dispute"})
	if s["suspended"] != true || s["suspended_reason"] != "payment dispute" {
		t.Fatalf("suspend = %v", s)
	}
	me := e.ok(200, "GET", "/v1/me", tok1, nil)
	if me["entitled"] != false {
		t.Fatal("a suspended org must not be entitled, even mid-trial")
	}
	if code, out := e.do("POST", "/v1/agents", tok1, map[string]any{"name": "X"}); code != 403 {
		t.Fatalf("suspended org could still act: %d %v", code, out)
	}

	u := e.ok(200, "POST", "/v1/admin/orgs/"+acmeID+"/unsuspend", "admintok", nil)
	// Both fields have `omitempty`, so a cleared value is simply absent from the JSON.
	if v, ok := u["suspended"]; ok && v != false {
		t.Fatalf("unsuspend = %v", u)
	}
	if _, ok := u["suspended_reason"]; ok {
		t.Fatalf("unsuspend left a reason behind: %v", u)
	}
	me = e.ok(200, "GET", "/v1/me", tok1, nil)
	if me["entitled"] != true {
		t.Fatal("unsuspending must restore entitlement")
	}

	// Unknown org.
	e.ok(404, "POST", "/v1/admin/orgs/does-not-exist/suspend", "admintok", map[string]any{"reason": "x"})
}

func TestAdminOrgsExcludeClientsAndPaginate(t *testing.T) {
	e := adminEnv(t, "admintok")
	agencyTok := e.signup("Bright Agency", "boss@bright.test")
	e.signup("Second Top-Level Org", "owner@second.test")
	e.setPlan(e.orgID(agencyTok), domain.PlanEnterprise)
	e.ok(201, "POST", "/v1/clients", agencyTok, map[string]any{
		"name": "Cedar Restaurants", "email": "owner@cedar.test", "password": "cedar-pass-1", "plan": "starter",
	})
	orgs := e.ok(200, "GET", "/v1/admin/orgs", "admintok", nil)["orgs"].([]any)
	if len(orgs) != 2 {
		t.Fatalf("orgs = %d, want 2 (agency + second), excluding the client", len(orgs))
	}
	for _, o := range orgs {
		if o.(map[string]any)["name"] == "Cedar Restaurants" {
			t.Fatal("a client org must not appear in the top-level admin list")
		}
	}
	page := e.ok(200, "GET", "/v1/admin/orgs?limit=1", "admintok", nil)
	if page["has_more"] != true || len(page["orgs"].([]any)) != 1 {
		t.Fatalf("page = %v", page)
	}
}

func TestAdminHealth(t *testing.T) {
	e := adminEnv(t, "admintok")
	e.signup("Acme", "a@acme.test")
	h := e.ok(200, "GET", "/v1/admin/health", "admintok", nil)
	deps := h["dependencies"].(map[string]any)
	if deps["database"].(map[string]any)["ok"] != true {
		t.Fatalf("health = %v", h)
	}
	if h["organizations"] != float64(1) {
		t.Fatalf("organizations = %v", h["organizations"])
	}
}

func TestHealthzReportsDependencyStatus(t *testing.T) {
	e := newEnv(t)
	code, body := e.raw("GET", "/healthz", "", "", nil)
	if code != 200 || !strings.Contains(body, `"status":"ok"`) || !strings.Contains(body, `"database"`) {
		t.Fatalf("healthz = %d %s", code, body)
	}
}

func TestOneClickUnsubscribe(t *testing.T) {
	e := newEnv(t)
	tok := e.signup("Acme", "a@acme.test")
	aid := e.agent(tok, nil)
	pid := e.prospect(tok, aid, "ABC Restaurant", "ahmed@abc.test")
	msg := e.ok(201, "POST", "/v1/prospects/"+pid+"/outreach", tok, nil)
	e.ok(200, "POST", "/v1/messages/"+str(msg, "id")+"/review", tok, map[string]any{"approve": true})

	sent := e.ch.Sent()
	if len(sent) != 1 {
		t.Fatalf("sent = %d", len(sent))
	}
	if !strings.Contains(sent[0].Body, "stop") { // the sent body goes through the footer+unsub URL
		t.Fatalf("body missing footer: %q", sent[0].Body)
	}

	unsubURL := sent[0].UnsubscribeURL
	if unsubURL == "" {
		t.Fatal("no UnsubscribeURL was set on the outgoing email")
	}
	path := unsubURL[strings.Index(unsubURL, "/v1/unsubscribe/"):]

	// One click (POST, per RFC 8058) opts the prospect out.
	code, body := e.raw("POST", path, "", "", nil)
	if code != 200 || !strings.Contains(body, "unsubscribed") {
		t.Fatalf("unsubscribe = %d %q", code, body)
	}
	if e.stageOf(tok, pid) != "do_not_contact" {
		t.Fatalf("stage = %s", e.stageOf(tok, pid))
	}
	// Clicking again (a retry, or a second click) must not error.
	if code, _ := e.raw("POST", path, "", "", nil); code != 200 {
		t.Fatalf("second click = %d", code)
	}
	// GET also works, for a human following the mailto/link fallback.
	if code, _ := e.raw("GET", path, "", "", nil); code != 200 {
		t.Fatalf("GET unsubscribe = %d", code)
	}

	// A forged token for a second, still-active prospect must answer 200 (never
	// leak whether a token is valid, so a provider's POST never sees an error)
	// but must NOT actually opt them out.
	pid2 := e.prospect(tok, aid, "Cedar Cafe", "rana@cedar.test")
	forged := path[:strings.LastIndex(path, "/")] // .../{org}/{prospect}/
	forged = forged[:strings.LastIndex(forged, "/")+1] + pid2 + "/0000000000000000000000000000000000000000000000000000000000000000"
	if code, _ := e.raw("POST", forged, "", "", nil); code != 200 {
		t.Fatalf("forged token = %d", code)
	}
	if e.stageOf(tok, pid2) == "do_not_contact" {
		t.Fatal("a forged token must not actually unsubscribe anyone")
	}
	// An unknown ORG fails before any token comparison, so it is a real 404.
	e.ok(404, "POST", "/v1/unsubscribe/does-not-exist/"+pid2+"/"+strings.Repeat("0", 64), "", nil)
	// An unknown prospect under a real org fails the token check first (same
	// prospect ID is part of what's HMAC'd), so it answers 200 like any other
	// wrong token — this is the opacity property, not a bug.
	e.ok(200, "POST", "/v1/unsubscribe/"+e.orgID(tok)+"/does-not-exist/"+strings.Repeat("0", 64), "", nil)
}
