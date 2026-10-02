package httpapi

import (
	"net/http"
	"time"
)

// Browser sessions use httpOnly cookies, not a token the page's JS can read —
// this is what stops a successful XSS from exfiltrating a session. Three cookies:
//
//   - aisp_session:  the JWT, httpOnly. Sent back on every request.
//   - aisp_csrf:     a random value, NOT httpOnly, double-submitted as the
//     X-CSRF-Token header on every mutating request. A page on another origin
//     can make the browser send the cookie, but it cannot read it to also set
//     the header (same-origin policy), so it cannot forge the pair.
//   - aisp_agency_session: httpOnly, only present while an agency is viewing a
//     client's dashboard — the agency's own session, so "back to my agency"
//     needs no fresh login.
//
// Non-browser clients (API keys, curl, tests) keep using the Authorization
// header exactly as before: requireAuth tries it first, and a request
// authenticated that way is exempt from the CSRF check (a cross-site page
// cannot set a custom header on a simple form submission, and cannot read or
// set Authorization at all without already running script on our origin).
const (
	cookieSession       = "aisp_session"
	cookieCSRF          = "aisp_csrf"
	cookieAgencySession = "aisp_agency_session"
	csrfHeader          = "X-CSRF-Token"
)

// setSessionCookies sets the session for a normal login. ttl overrides the
// default (used for a short-lived agency impersonation session).
func (a *API) setSessionCookies(w http.ResponseWriter, tok string, ttl time.Duration) {
	a.setCookie(w, cookieSession, tok, true, ttl)
	a.setCookie(w, cookieCSRF, randomHex(16), false, ttl)
}

func (a *API) clearSessionCookies(w http.ResponseWriter) {
	a.clearCookie(w, cookieSession)
	a.clearCookie(w, cookieCSRF)
	a.clearCookie(w, cookieAgencySession)
}

func (a *API) setCookie(w http.ResponseWriter, name, value string, httpOnly bool, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: value, Path: "/", HttpOnly: httpOnly, Secure: a.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode, MaxAge: int(ttl.Seconds()),
	})
}

func (a *API) clearCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: "", Path: "/", HttpOnly: true, Secure: a.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
}
