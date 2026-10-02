package httpapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"testing"

	"github.com/hussein/ai-salesperson/internal/domain"
)

// browser is a cookie-jar-backed client standing in for the dashboard: it
// relies entirely on cookies, never an Authorization header, exactly like the
// web app after the localStorage -> httpOnly cookie change.
type browser struct {
	t    *testing.T
	base string
	cl   *http.Client
}

func newBrowser(t *testing.T, e *env) *browser {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &browser{t: t, base: e.srv.URL, cl: &http.Client{Jar: jar}}
}

func (b *browser) cookie(name string) string {
	u, _ := url.Parse(b.base)
	for _, c := range b.cl.Jar.Cookies(u) {
		if c.Name == name {
			return c.Value
		}
	}
	return ""
}

// do sends a request. withCSRF controls whether the X-CSRF-Token header is
// sent from the jar's csrf cookie, so a test can deliberately omit or forge it.
func (b *browser) do(method, path string, body any, withCSRF bool) (int, map[string]any) {
	b.t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rdr = bytes.NewReader(raw)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, b.base+path, rdr)
	if err != nil {
		b.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if withCSRF {
		req.Header.Set("X-CSRF-Token", b.cookie("aisp_csrf"))
	}
	resp, err := b.cl.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// rawDo exposes the actual response so a test can inspect Set-Cookie
// attributes (HttpOnly, SameSite, …) that the cookie jar deliberately does not
// preserve — a jar only tracks what's needed to resend cookies, not the
// browser-enforcement attributes.
func (b *browser) rawDo(method, path string, body any) *http.Response {
	b.t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rdr = bytes.NewReader(raw)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, b.base+path, rdr)
	if err != nil {
		b.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := b.cl.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	return resp
}

func (b *browser) ok(want int, method, path string, body any) map[string]any {
	b.t.Helper()
	code, out := b.do(method, path, body, true)
	if code != want {
		b.t.Fatalf("%s %s: status %d, want %d; body=%v", method, path, code, want, out)
	}
	return out
}

func TestCookieLoginSetsHttpOnlySessionAndCSRFCookies(t *testing.T) {
	e := newEnv(t)
	b := newBrowser(t, e)
	raw := b.rawDo("POST", "/v1/auth/signup", map[string]any{"org_name": "Acme", "email": "a@acme.test", "password": "password123"})
	defer raw.Body.Close()
	var body map[string]any
	_ = json.NewDecoder(raw.Body).Decode(&body)
	if raw.StatusCode != 201 {
		t.Fatalf("signup = %d %v", raw.StatusCode, body)
	}
	if str(body, "token") == "" {
		t.Fatal("the response body must still carry a bearer token for API/CLI clients")
	}
	var session, csrf *http.Cookie
	for _, c := range raw.Cookies() {
		switch c.Name {
		case "aisp_session":
			session = c
		case "aisp_csrf":
			csrf = c
		}
	}
	if session == nil || csrf == nil {
		t.Fatalf("missing cookies: session=%v csrf=%v", session, csrf)
	}
	if !session.HttpOnly {
		t.Error("the session cookie must be httpOnly")
	}
	if csrf.HttpOnly {
		t.Error("the csrf cookie must be readable by the page's JS (double-submit)")
	}
	if session.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want Lax", session.SameSite)
	}
	if session.Value == "" || csrf.Value == "" {
		t.Error("cookie values must not be empty")
	}

	// Cookie alone (no Authorization header) authenticates GET requests.
	b.ok(200, "GET", "/v1/me", nil)
}

func TestCSRFIsRequiredForMutatingCookieRequests(t *testing.T) {
	e := newEnv(t)
	b := newBrowser(t, e)
	b.ok(201, "POST", "/v1/auth/signup", map[string]any{"org_name": "Acme", "email": "a@acme.test", "password": "password123"})

	// No CSRF header at all.
	if code, out := b.do("POST", "/v1/agents", map[string]any{"name": "X"}, false); code != 403 {
		t.Fatalf("missing CSRF header: %d %v", code, out)
	}
	// A forged header that doesn't match the cookie.
	req, _ := http.NewRequest("POST", e.srv.URL+"/v1/agents", bytes.NewReader([]byte(`{"name":"X"}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", "not-the-real-value")
	resp, err := b.cl.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("forged CSRF header: %d", resp.StatusCode)
	}
	// The matching header from the cookie jar succeeds.
	b.ok(201, "POST", "/v1/agents", map[string]any{"name": "X"})
	// GET never needs it.
	b.do("GET", "/v1/agents", nil, false)
}

func TestBearerTokenIsExemptFromCSRF(t *testing.T) {
	e := newEnv(t)
	tok := e.signup("Acme", "a@acme.test")
	// The existing header-based path (API clients, curl, every other test in
	// this package) must keep working with no CSRF header at all.
	e.ok(201, "POST", "/v1/agents", tok, map[string]any{"name": "X"})
}

func TestLogoutClearsCookiesAndEndsTheSession(t *testing.T) {
	e := newEnv(t)
	b := newBrowser(t, e)
	b.ok(201, "POST", "/v1/auth/signup", map[string]any{"org_name": "Acme", "email": "a@acme.test", "password": "password123"})
	b.ok(200, "GET", "/v1/me", nil)

	if code, _ := b.do("POST", "/v1/auth/logout", nil, true); code != 204 {
		t.Fatalf("logout = %d", code)
	}
	u, _ := url.Parse(e.srv.URL)
	for _, c := range b.cl.Jar.Cookies(u) {
		if c.Name == "aisp_session" || c.Name == "aisp_csrf" {
			t.Errorf("cookie %s survived logout", c.Name)
		}
	}
	if code, _ := b.do("GET", "/v1/me", nil, true); code != 401 {
		t.Fatalf("after logout = %d, want 401", code)
	}
}

func TestAgencyEnterAndExitClientViaCookiesOnly(t *testing.T) {
	e := newEnv(t)
	agency := newBrowser(t, e)
	agency.ok(201, "POST", "/v1/auth/signup", map[string]any{"org_name": "Bright Agency", "email": "boss@bright.test", "password": "password123"})
	agencyOrgID := str(agency.ok(200, "GET", "/v1/me", nil)["org"].(map[string]any), "id")
	o, err := e.st.GetOrg(e.t.Context(), agencyOrgID)
	if err != nil {
		t.Fatal(err)
	}
	o.Plan = domain.PlanEnterprise
	if err := e.st.UpdateOrg(e.t.Context(), o); err != nil {
		t.Fatal(err)
	}

	client := agency.ok(201, "POST", "/v1/clients", map[string]any{
		"name": "Cedar Restaurants", "email": "owner@cedar.test", "password": "cedar-pass-1", "plan": "starter",
	})
	clientID := str(client, "id")

	login := agency.ok(200, "POST", "/v1/clients/"+clientID+"/login", nil)
	if str(login, "token") == "" {
		t.Fatal("clientLogin must still return a token in the body for non-browser use")
	}
	// The cookie jar now holds the impersonation session; no token from the
	// response body was ever used to get here.
	me := agency.ok(200, "GET", "/v1/me", nil)
	if me["org"].(map[string]any)["id"] != clientID || me["agency_session"] != true {
		t.Fatalf("me while impersonating = %v", me)
	}
	agency.ok(200, "GET", "/v1/agents", nil) // can act inside the client

	// Exit restores the agency's own session purely from cookies.
	back := agency.ok(200, "POST", "/v1/clients/exit", nil)
	if back["org"].(map[string]any)["id"] != agencyOrgID {
		t.Fatalf("exit = %v", back)
	}
	me = agency.ok(200, "GET", "/v1/me", nil)
	if me["org"].(map[string]any)["id"] != agencyOrgID || me["agency_session"] != false {
		t.Fatalf("me after exit = %v", me)
	}
	agency.ok(200, "GET", "/v1/clients", nil) // agency-only action works again

	// Exiting again with nothing stashed is a clean error, not a crash.
	if code, _ := agency.do("POST", "/v1/clients/exit", nil, true); code != 409 {
		t.Fatalf("exit with nothing to return to = %d", code)
	}
}
