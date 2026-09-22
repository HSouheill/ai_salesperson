package httpapi

import (
	"io"
	"log"
	"net/http"
	"time"

	"github.com/hussein/ai-salesperson/internal/billing"
	"github.com/hussein/ai-salesperson/internal/domain"
)

func (a *API) checkout(w http.ResponseWriter, r *http.Request) {
	if !interactive(w, r) {
		return
	}
	var in struct {
		Plan domain.Plan `json:"plan"`
	}
	if !decode(w, r, &in) {
		return
	}
	o, err := a.store.GetOrg(r.Context(), orgID(r))
	if err != nil {
		fail(w, err)
		return
	}
	if o.ParentID != "" {
		writeErr(w, http.StatusForbidden, "your plan is managed by your agency")
		return
	}
	if in.Plan != domain.PlanStarter && in.Plan != domain.PlanGrowth && in.Plan != domain.PlanPro {
		writeErr(w, http.StatusBadRequest, "plan must be starter, growth or pro (Enterprise: contact sales)")
		return
	}
	u, err := a.stripe.CheckoutURL(r.Context(), o, "", in.Plan, a.cfg.WebURL+"/billing?status=success", a.cfg.WebURL+"/billing?status=cancelled")
	if err != nil {
		if err == billing.ErrNotConfigured {
			fail(w, err)
			return
		}
		log.Printf("checkout: %v", err)
		writeErr(w, http.StatusBadGateway, "could not start checkout: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": u})
}

func (a *API) portal(w http.ResponseWriter, r *http.Request) {
	if !interactive(w, r) {
		return
	}
	o, err := a.store.GetOrg(r.Context(), orgID(r))
	if err != nil {
		fail(w, err)
		return
	}
	if o.Billing.CustomerID == "" {
		writeErr(w, http.StatusConflict, "there is no subscription to manage yet")
		return
	}
	u, err := a.stripe.PortalURL(r.Context(), o.Billing.CustomerID, a.cfg.WebURL+"/billing")
	if err != nil {
		if err == billing.ErrNotConfigured {
			fail(w, err)
			return
		}
		writeErr(w, http.StatusBadGateway, "could not open the billing portal: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": u})
}

// billingWebhook receives Stripe events. The signature is verified first, so
// nothing is trusted from an unsigned request.
func (a *API) billingWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "unreadable body")
		return
	}
	if err := a.stripe.VerifyWebhook(body, r.Header.Get("Stripe-Signature"), time.Now()); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid signature")
		return
	}
	ev, err := billing.ParseEvent(body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid event")
		return
	}
	id := ev.Object.Metadata["org_id"]
	if id == "" {
		id = ev.Object.ClientReferenceID
	}
	if id == "" {
		w.WriteHeader(http.StatusOK) // not one of ours
		return
	}
	o, err := a.store.GetOrg(r.Context(), id)
	if err != nil {
		log.Printf("billing webhook %s: unknown org %s", ev.Type, id)
		w.WriteHeader(http.StatusOK) // unknown org: retrying will not help
		return
	}
	if billing.Apply(&o, ev) {
		if err := a.store.UpdateOrg(r.Context(), o); err != nil {
			fail(w, err) // Stripe will retry
			return
		}
		log.Printf("billing: org %s -> plan=%s status=%s (%s)", o.ID, o.Plan, o.Billing.Status, ev.Type)
	}
	w.WriteHeader(http.StatusOK)
}
