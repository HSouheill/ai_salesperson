package httpapi

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/hussein/ai-salesperson/internal/auth"
	"github.com/hussein/ai-salesperson/internal/domain"
	"github.com/hussein/ai-salesperson/internal/sales"
)

// White-label: an Enterprise agency creates client organizations, each with its
// own dashboard, agents, campaigns and CRM data, under the agency's branding.

const maxClients = 200

var (
	reSlug  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,38}[a-z0-9]$`)
	reColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
)

func (a *API) requireAgency(w http.ResponseWriter, r *http.Request) (domain.Org, bool) {
	if who(r).Agency != "" || who(r).viaAPIKey {
		writeErr(w, http.StatusForbidden, "this action needs a direct login to the agency account")
		return domain.Org{}, false
	}
	o, err := a.store.GetOrg(r.Context(), orgID(r))
	if err != nil {
		fail(w, err)
		return o, false
	}
	if o.ParentID != "" || !o.Plan.Limits().WhiteLabel {
		writeErr(w, http.StatusForbidden, "white-label needs the Enterprise plan")
		return o, false
	}
	return o, true
}

func validBranding(b domain.Branding) string {
	if len(b.ProductName) > 60 {
		return "product_name too long"
	}
	if b.PrimaryColor != "" && !reColor.MatchString(b.PrimaryColor) {
		return "primary_color must look like #3b5bdb"
	}
	if b.LogoURL != "" {
		u, err := url.Parse(b.LogoURL)
		if err != nil || u.Scheme != "https" || u.Host == "" || len(b.LogoURL) > 500 {
			return "logo_url must be an https URL"
		}
	}
	return ""
}

type clientView struct {
	Org   domain.Org   `json:"org"`
	Stats domain.Stats `json:"stats"`
}

func (a *API) listClients(w http.ResponseWriter, r *http.Request) {
	agency, ok := a.requireAgency(w, r)
	if !ok {
		return
	}
	kids, err := a.store.ListChildOrgs(r.Context(), agency.ID)
	if err != nil {
		fail(w, err)
		return
	}
	out := make([]clientView, 0, len(kids))
	for _, k := range kids {
		st, err := a.sales.Stats(r.Context(), k.ID)
		if err != nil {
			fail(w, err)
			return
		}
		out = append(out, clientView{Org: k, Stats: st})
	}
	writeJSON(w, http.StatusOK, out)
}

func clientPlan(p domain.Plan) bool {
	return p == domain.PlanStarter || p == domain.PlanGrowth || p == domain.PlanPro
}

func (a *API) createClient(w http.ResponseWriter, r *http.Request) {
	agency, ok := a.requireAgency(w, r)
	if !ok {
		return
	}
	var in struct {
		Name     string          `json:"name"`
		Slug     string          `json:"slug"`
		Email    string          `json:"email"`
		Password string          `json:"password"`
		Plan     domain.Plan     `json:"plan"`
		Branding domain.Branding `json:"branding"`
	}
	if !decode(w, r, &in) {
		return
	}
	email, okEmail := validEmail(in.Email)
	slug := strings.ToLower(strings.TrimSpace(in.Slug))
	if in.Plan == "" {
		in.Plan = domain.PlanStarter
	}
	switch {
	case strings.TrimSpace(in.Name) == "" || len(in.Name) > 100:
		writeErr(w, http.StatusBadRequest, "name is required (max 100 characters)")
		return
	case !okEmail:
		writeErr(w, http.StatusBadRequest, "a valid email for the client's first user is required")
		return
	case !validPassword(in.Password):
		writeErr(w, http.StatusBadRequest, "password must be 8-72 characters")
		return
	case slug != "" && !reSlug.MatchString(slug):
		writeErr(w, http.StatusBadRequest, "slug must be 3-40 characters: lowercase letters, digits and dashes")
		return
	case !clientPlan(in.Plan):
		writeErr(w, http.StatusBadRequest, "plan must be starter, growth or pro")
		return
	}
	if msg := validBranding(in.Branding); msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	kids, err := a.store.ListChildOrgs(r.Context(), agency.ID)
	if err != nil {
		fail(w, err)
		return
	}
	if len(kids) >= maxClients {
		writeErr(w, http.StatusForbidden, "client limit reached")
		return
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		fail(w, err)
		return
	}
	now := time.Now()
	org := domain.Org{ID: sales.NewID(), Name: strings.TrimSpace(in.Name), Plan: in.Plan, ParentID: agency.ID, Slug: slug,
		Branding: in.Branding, CreatedAt: now, InboundToken: randomHex(24)}
	u := domain.User{ID: sales.NewID(), OrgID: org.ID, Email: email, PasswordHash: hash, CreatedAt: now}
	if err := a.store.CreateOrg(r.Context(), org, u); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, org)
}

func (a *API) ownClient(w http.ResponseWriter, r *http.Request, agency domain.Org) (domain.Org, bool) {
	c, err := a.store.GetOrg(r.Context(), r.PathValue("id"))
	if err != nil || c.ParentID != agency.ID { // other agencies' clients look like they do not exist
		writeErr(w, http.StatusNotFound, "not found")
		return c, false
	}
	return c, true
}

func (a *API) updateClient(w http.ResponseWriter, r *http.Request) {
	agency, ok := a.requireAgency(w, r)
	if !ok {
		return
	}
	c, ok := a.ownClient(w, r, agency)
	if !ok {
		return
	}
	var in struct {
		Name     *string          `json:"name"`
		Plan     *domain.Plan     `json:"plan"`
		Branding *domain.Branding `json:"branding"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Name != nil {
		if n := strings.TrimSpace(*in.Name); n == "" || len(n) > 100 {
			writeErr(w, http.StatusBadRequest, "name is required (max 100 characters)")
			return
		}
		c.Name = strings.TrimSpace(*in.Name)
	}
	if in.Plan != nil {
		if !clientPlan(*in.Plan) {
			writeErr(w, http.StatusBadRequest, "plan must be starter, growth or pro")
			return
		}
		c.Plan = *in.Plan
	}
	if in.Branding != nil {
		if msg := validBranding(*in.Branding); msg != "" {
			writeErr(w, http.StatusBadRequest, msg)
			return
		}
		c.Branding = *in.Branding
	}
	if err := a.store.UpdateOrg(r.Context(), c); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

// clientLogin gives the agency a short session inside a client's dashboard.
func (a *API) clientLogin(w http.ResponseWriter, r *http.Request) {
	agency, ok := a.requireAgency(w, r)
	if !ok {
		return
	}
	c, ok := a.ownClient(w, r, agency)
	if !ok {
		return
	}
	tok, err := auth.Sign(a.cfg.JWTSecret, auth.Claims{UserID: who(r).UserID, OrgID: c.ID, Agency: agency.ID}, impersonateTTL)
	if err != nil {
		fail(w, err)
		return
	}
	logf("agency %s opened client %s", agency.ID, c.ID) // audit trail
	writeJSON(w, http.StatusOK, map[string]any{"token": tok, "org": c})
}
