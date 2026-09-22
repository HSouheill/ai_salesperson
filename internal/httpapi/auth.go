package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/hussein/ai-salesperson/internal/auth"
	"github.com/hussein/ai-salesperson/internal/domain"
	"github.com/hussein/ai-salesperson/internal/sales"
)

const (
	tokenTTL       = 24 * time.Hour
	impersonateTTL = 2 * time.Hour
	trialLength    = 14 * 24 * time.Hour
	apiKeyPrefix   = "aisp_"
)

type ctxKey struct{}

type principal struct {
	auth.Claims
	viaAPIKey bool
}

func who(r *http.Request) principal { return r.Context().Value(ctxKey{}).(principal) }

// orgID returns the authenticated organization; every handler scopes by it.
func orgID(r *http.Request) string { return who(r).OrgID }

func hashKey(k string) string {
	h := sha256.Sum256([]byte(k))
	return hex.EncodeToString(h[:])
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (a *API) requireAuth(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok {
			writeErr(w, http.StatusUnauthorized, "missing bearer token")
			return
		}
		var p principal
		if strings.HasPrefix(tok, apiKeyPrefix) {
			k, err := a.store.APIKeyByHash(r.Context(), hashKey(tok))
			if err != nil {
				writeErr(w, http.StatusUnauthorized, "invalid API key")
				return
			}
			org, err := a.store.GetOrg(r.Context(), k.OrgID)
			if err != nil || !org.Plan.Limits().APIAccess {
				writeErr(w, http.StatusForbidden, "API access needs the Pro plan")
				return
			}
			p = principal{Claims: auth.Claims{UserID: "apikey:" + k.ID, OrgID: k.OrgID}, viaAPIKey: true}
		} else {
			c, err := auth.Verify(a.cfg.JWTSecret, tok)
			if err != nil {
				writeErr(w, http.StatusUnauthorized, "invalid or expired token")
				return
			}
			p = principal{Claims: c}
		}
		next(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, p)))
	})
}

// interactive rejects API keys and agency impersonation sessions for actions
// that must be done by the account owner themself (keys, billing).
func interactive(w http.ResponseWriter, r *http.Request) bool {
	p := who(r)
	if p.viaAPIKey || p.Agency != "" {
		writeErr(w, http.StatusForbidden, "this action needs a direct login to the account")
		return false
	}
	return true
}

type credentials struct {
	OrgName  string `json:"org_name"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (a *API) token(u domain.User) (string, error) {
	return auth.Sign(a.cfg.JWTSecret, auth.Claims{UserID: u.ID, OrgID: u.OrgID}, tokenTTL)
}

func validEmail(s string) (string, bool) {
	addr, err := mail.ParseAddress(strings.TrimSpace(s))
	if err != nil || addr.Address != strings.TrimSpace(s) {
		return "", false
	}
	return addr.Address, true
}

func validPassword(s string) bool { return len(s) >= 8 && len(s) <= 72 } // bcrypt ignores input past 72 bytes

func (a *API) signup(w http.ResponseWriter, r *http.Request) {
	var in credentials
	if !decode(w, r, &in) {
		return
	}
	email, ok := validEmail(in.Email)
	switch {
	case !ok:
		writeErr(w, http.StatusBadRequest, "a valid email is required")
		return
	case !validPassword(in.Password):
		writeErr(w, http.StatusBadRequest, "password must be 8-72 characters")
		return
	case strings.TrimSpace(in.OrgName) == "" || len(in.OrgName) > 100:
		writeErr(w, http.StatusBadRequest, "org_name is required (max 100 characters)")
		return
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		fail(w, err)
		return
	}
	now := time.Now()
	ends := now.Add(trialLength)
	org := domain.Org{ID: sales.NewID(), Name: strings.TrimSpace(in.OrgName), Plan: domain.PlanTrial, TrialEndsAt: &ends,
		CreatedAt: now, InboundToken: randomHex(24)}
	u := domain.User{ID: sales.NewID(), OrgID: org.ID, Email: email, PasswordHash: hash, CreatedAt: now}
	if err := a.store.CreateOrg(r.Context(), org, u); err != nil {
		fail(w, err)
		return
	}
	tok, err := a.token(u)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"token": tok, "org": org, "user": u})
}

// dummyHash lets login do the same bcrypt work for unknown emails, so response
// time does not reveal which emails are registered.
var dummyHash, _ = auth.HashPassword("dummy-password")

func (a *API) login(w http.ResponseWriter, r *http.Request) {
	var in credentials
	if !decode(w, r, &in) {
		return
	}
	u, err := a.store.UserByEmail(r.Context(), in.Email)
	hash := dummyHash
	if err == nil {
		hash = u.PasswordHash
	}
	if !auth.CheckPassword(hash, in.Password) || err != nil {
		writeErr(w, http.StatusUnauthorized, "invalid email or password")
		return
	}
	tok, err := a.token(u)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": tok, "user": u})
}

func (a *API) plans(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, domain.PublicPlans())
}

// publicBranding lets a client's branded login page show the agency's name,
// logo and colour before anyone signs in. It exposes nothing else.
func (a *API) publicBranding(w http.ResponseWriter, r *http.Request) {
	o, err := a.store.OrgBySlug(r.Context(), strings.ToLower(r.URL.Query().Get("slug")))
	if err != nil {
		writeJSON(w, http.StatusOK, domain.Branding{})
		return
	}
	writeJSON(w, http.StatusOK, o.Branding)
}

func (a *API) me(w http.ResponseWriter, r *http.Request) {
	o, err := a.store.GetOrg(r.Context(), orgID(r))
	if err != nil {
		fail(w, err)
		return
	}
	entitled := o.Entitled(time.Now())
	if o.ParentID != "" {
		if parent, err := a.store.GetOrg(r.Context(), o.ParentID); err == nil && !parent.Entitled(time.Now()) {
			entitled = false
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"org": o, "entitled": entitled, "limits": o.Plan.Limits(),
		"agency_session": who(r).Agency != "", "is_client": o.ParentID != "",
		"billing_available": a.stripe.Configured(),
	})
}

// ---- API keys (Pro and above) ----------------------------------------------

func (a *API) requireAPIPlan(w http.ResponseWriter, r *http.Request) bool {
	o, err := a.store.GetOrg(r.Context(), orgID(r))
	if err != nil {
		fail(w, err)
		return false
	}
	if !o.Plan.Limits().APIAccess {
		writeErr(w, http.StatusForbidden, "API access needs the Pro plan")
		return false
	}
	return true
}

func (a *API) listAPIKeys(w http.ResponseWriter, r *http.Request) {
	if !interactive(w, r) {
		return
	}
	ks, err := a.store.ListAPIKeys(r.Context(), orgID(r))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ks)
}

func (a *API) createAPIKey(w http.ResponseWriter, r *http.Request) {
	if !interactive(w, r) || !a.requireAPIPlan(w, r) {
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Name) == "" || len(in.Name) > 60 {
		writeErr(w, http.StatusBadRequest, "name is required (max 60 characters)")
		return
	}
	existing, err := a.store.ListAPIKeys(r.Context(), orgID(r))
	if err != nil {
		fail(w, err)
		return
	}
	if len(existing) >= 20 {
		writeErr(w, http.StatusForbidden, "at most 20 API keys per organization")
		return
	}
	key := apiKeyPrefix + randomHex(24)
	k := domain.APIKey{ID: sales.NewID(), OrgID: orgID(r), Name: strings.TrimSpace(in.Name), Prefix: key[:len(apiKeyPrefix)+6], Hash: hashKey(key), CreatedAt: time.Now()}
	if err := a.store.PutAPIKey(r.Context(), k); err != nil {
		fail(w, err)
		return
	}
	// The full key is shown once; only its hash is stored.
	writeJSON(w, http.StatusCreated, map[string]any{"key": key, "api_key": k})
}

func (a *API) deleteAPIKey(w http.ResponseWriter, r *http.Request) {
	if !interactive(w, r) {
		return
	}
	if err := a.store.DeleteAPIKey(r.Context(), orgID(r), r.PathValue("id")); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
