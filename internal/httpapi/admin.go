package httpapi

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hussein/ai-salesperson/internal/domain"
)

// Operator admin API: list/suspend organizations and check system health.
// Authenticated by a single static bearer token (ADMIN_TOKEN) rather than a
// per-user login — this is an operator tool, not a customer-facing role, and
// a static shared secret matches how the `set-plan` CLI command already works.
// With no ADMIN_TOKEN configured the whole surface answers 503, closed by default.

func (a *API) requireAdmin(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.cfg.AdminToken == "" {
			writeErr(w, http.StatusServiceUnavailable, "the admin API is not configured (set ADMIN_TOKEN)")
			return
		}
		tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || subtle.ConstantTimeCompare([]byte(tok), []byte(a.cfg.AdminToken)) != 1 {
			writeErr(w, http.StatusUnauthorized, "invalid admin token")
			return
		}
		next(w, r)
	})
}

type adminOrgView struct {
	domain.Org
	UserEmail string `json:"user_email,omitempty"`
}

func (a *API) adminListOrgs(w http.ResponseWriter, r *http.Request) {
	limit := clampInt(r.URL.Query().Get("limit"), 50, 1, 200)
	offset := clampInt(r.URL.Query().Get("offset"), 0, 0, 1_000_000)
	orgs, err := a.store.ListOrgs(r.Context(), r.URL.Query().Get("q"), limit+1, offset)
	if err != nil {
		fail(w, err)
		return
	}
	hasMore := len(orgs) > limit
	if hasMore {
		orgs = orgs[:limit]
	}
	out := make([]adminOrgView, len(orgs))
	for i, o := range orgs {
		out[i] = adminOrgView{Org: o}
	}
	writeJSON(w, http.StatusOK, map[string]any{"orgs": out, "has_more": hasMore})
}

func clampInt(s string, def, min, max int) int {
	n, err := strconv.Atoi(s)
	if err != nil || n < min {
		return def
	}
	if n > max {
		return max
	}
	return n
}

func (a *API) adminSuspend(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Reason string `json:"reason"`
	}
	if !decode(w, r, &in) {
		return
	}
	o, err := a.store.GetOrg(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	o.Suspended, o.SuspendedReason = true, strings.TrimSpace(in.Reason)
	if err := a.store.UpdateOrg(r.Context(), o); err != nil {
		fail(w, err)
		return
	}
	logf("admin: suspended org %s (%s): %s", o.ID, o.Name, o.SuspendedReason)
	writeJSON(w, http.StatusOK, o)
}

func (a *API) adminUnsuspend(w http.ResponseWriter, r *http.Request) {
	o, err := a.store.GetOrg(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	o.Suspended, o.SuspendedReason = false, ""
	if err := a.store.UpdateOrg(r.Context(), o); err != nil {
		fail(w, err)
		return
	}
	logf("admin: unsuspended org %s (%s)", o.ID, o.Name)
	writeJSON(w, http.StatusOK, o)
}

// adminHealth reports what a real uptime monitor can't see from the outside:
// whether dependencies are actually reachable and roughly how much is running.
// Queue depth isn't included — the queue backends don't expose it yet.
func (a *API) adminHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	dep := a.checkDependencies(ctx)
	orgs, _ := a.store.CountOrgs(ctx)
	active, _ := a.store.ListActiveAgents(ctx)
	inboxes, _ := a.store.ListInboxOrgs(ctx)
	writeJSON(w, http.StatusOK, map[string]any{
		"dependencies": dep, "organizations": orgs, "active_agents": len(active), "polled_inboxes": len(inboxes),
		"ai_provider": a.cfg.AIProvider, "scheduler_interval": a.cfg.SchedulerInterval.String(),
		"billing_configured": a.stripe.Configured(),
	})
}
