// Package httpapi exposes the platform as a JSON REST API.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/hussein/ai-salesperson/internal/ai"
	"github.com/hussein/ai-salesperson/internal/billing"
	"github.com/hussein/ai-salesperson/internal/channels"
	"github.com/hussein/ai-salesperson/internal/config"
	"github.com/hussein/ai-salesperson/internal/inbound"
	"github.com/hussein/ai-salesperson/internal/jobs"
	"github.com/hussein/ai-salesperson/internal/queue"
	"github.com/hussein/ai-salesperson/internal/sales"
	"github.com/hussein/ai-salesperson/internal/store"
)

type API struct {
	cfg         config.Config
	store       store.Store
	sales       *sales.Service
	jobs        *jobs.Runner
	queue       *queue.Queue
	poller      *inbound.Poller
	stripe      *billing.Stripe
	authRL      *limiter
	testLimiter *limiter
}

func New(cfg config.Config, st store.Store, svc *sales.Service, jr *jobs.Runner, q *queue.Queue, poller *inbound.Poller, stripe *billing.Stripe) http.Handler {
	a := &API{cfg: cfg, store: st, sales: svc, jobs: jr, queue: q, poller: poller, stripe: stripe, authRL: newLimiter(10, time.Minute), testLimiter: newLimiter(5, time.Minute)}
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("GET /v1/plans", a.plans)
	mux.HandleFunc("GET /v1/public/branding", a.publicBranding)
	mux.Handle("POST /v1/auth/signup", a.rateLimited(http.HandlerFunc(a.signup)))
	mux.Handle("POST /v1/auth/login", a.rateLimited(http.HandlerFunc(a.login)))

	// Provider callbacks: authenticated by an unguessable per-org token or a signature, not a login.
	mux.HandleFunc("POST /v1/inbound/email/{token}", a.inboundEmail)
	mux.HandleFunc("GET /v1/inbound/whatsapp/{token}", a.whatsappVerify)
	mux.HandleFunc("POST /v1/inbound/whatsapp/{token}", a.whatsappInbound)
	mux.HandleFunc("POST /v1/billing/webhook", a.billingWebhook)

	authed := func(pattern string, h http.HandlerFunc) { mux.Handle(pattern, a.requireAuth(h)) }
	authed("GET /v1/me", a.me)
	authed("GET /v1/dashboard", a.dashboard)
	authed("POST /v1/onboarding/analyze", a.analyzeWebsite)
	authed("GET /v1/sources", a.listSources)

	authed("GET /v1/settings", a.getSettings)
	authed("PUT /v1/settings", a.putSettings)
	authed("POST /v1/settings/test", a.testSettings)
	authed("POST /v1/settings/rotate-inbound-token", a.rotateInboundToken)

	authed("GET /v1/api-keys", a.listAPIKeys)
	authed("POST /v1/api-keys", a.createAPIKey)
	authed("DELETE /v1/api-keys/{id}", a.deleteAPIKey)

	authed("POST /v1/billing/checkout", a.checkout)
	authed("POST /v1/billing/portal", a.portal)

	authed("GET /v1/clients", a.listClients)
	authed("POST /v1/clients", a.createClient)
	authed("PUT /v1/clients/{id}", a.updateClient)
	authed("POST /v1/clients/{id}/login", a.clientLogin)

	authed("GET /v1/agents", a.listAgents)
	authed("POST /v1/agents", a.createAgent)
	authed("GET /v1/agents/{id}", a.getAgent)
	authed("PUT /v1/agents/{id}", a.updateAgent)
	authed("POST /v1/agents/{id}/brain", a.generateBrain)
	authed("POST /v1/agents/{id}/status", a.setStatus)
	authed("POST /v1/agents/{id}/prospects", a.importProspects)
	authed("GET /v1/agents/{id}/prospects", a.listProspects)
	authed("POST /v1/agents/{id}/research", a.researchAll)
	authed("POST /v1/agents/{id}/discover", a.discover)
	authed("POST /v1/agents/{id}/autopilot/run", a.runAutopilot)
	authed("POST /v1/agents/{id}/followups", a.runFollowUps)

	authed("GET /v1/prospects/{id}", a.getProspect)
	authed("POST /v1/prospects/{id}/research", a.researchOne)
	authed("POST /v1/prospects/{id}/outreach", a.outreach)
	authed("POST /v1/prospects/{id}/replies", a.reply)
	authed("POST /v1/prospects/{id}/meeting", a.bookMeeting)
	authed("POST /v1/prospects/{id}/convert", a.convert)
	authed("PUT /v1/prospects/{id}/deal", a.setDeal)
	authed("POST /v1/prospects/{id}/lost", a.markLost)

	authed("GET /v1/messages", a.listMessages)
	authed("POST /v1/messages/{id}/review", a.reviewMessage)
	authed("GET /v1/jobs/{id}", a.getJob)

	return a.cors(logRequests(mux))
}

func (a *API) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if o := a.cfg.CORSOrigin; o != "" && r.Header.Get("Origin") == o {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", o)
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			h.Set("Vary", "Origin")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	code int
}

func (s *statusWriter) WriteHeader(c int) { s.code = c; s.ResponseWriter.WriteHeader(c) }

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, code: 200}
		next.ServeHTTP(sw, r)
		path := r.URL.Path
		if strings.HasPrefix(path, "/v1/inbound/") { // the token in the path is a credential
			path = path[:strings.LastIndex(path, "/")] + "/…"
		}
		log.Printf("%s %s %d %s", r.Method, path, sw.code, time.Since(start).Round(time.Millisecond))
	})
}

// ---- rate limiting (per client IP, in memory) --------------------------------

type limiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	hits   map[string][]time.Time
}

func newLimiter(max int, window time.Duration) *limiter {
	return &limiter{max: max, window: window, hits: map[string][]time.Time{}}
}

func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	recent := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if now.Sub(t) < l.window {
			recent = append(recent, t)
		}
	}
	if len(recent) >= l.max {
		l.hits[key] = recent
		return false
	}
	l.hits[key] = append(recent, now)
	if len(l.hits) > 10000 { // bound memory
		for k, v := range l.hits {
			if len(v) == 0 || now.Sub(v[len(v)-1]) > l.window {
				delete(l.hits, k)
			}
		}
	}
	return true
}

func (a *API) clientIP(r *http.Request) string {
	if a.cfg.TrustProxy {
		if xf := r.Header.Get("X-Forwarded-For"); xf != "" {
			return strings.TrimSpace(strings.Split(xf, ",")[0])
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (a *API) rateLimited(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.authRL.allow(a.clientIP(r)) {
			w.Header().Set("Retry-After", "60")
			writeErr(w, http.StatusTooManyRequests, "too many attempts; try again in a minute")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ---- responses ------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// fail maps service errors to HTTP statuses. Unexpected errors are logged and
// reported generically so internals do not leak.
func fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, http.StatusNotFound, "not found")
	case errors.Is(err, store.ErrConflict):
		writeErr(w, http.StatusConflict, "already exists")
	case errors.Is(err, sales.ErrInvalid):
		writeErr(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, sales.ErrPrecondition):
		writeErr(w, http.StatusConflict, err.Error())
	case errors.Is(err, sales.ErrLimit):
		writeErr(w, http.StatusForbidden, err.Error())
	case errors.Is(err, queue.ErrBusy):
		writeErr(w, http.StatusServiceUnavailable, "server is busy, try again shortly")
	case errors.Is(err, sales.ErrDelivery):
		writeErr(w, http.StatusBadGateway, err.Error())
	case errors.Is(err, sales.ErrSource):
		writeErr(w, http.StatusBadGateway, err.Error())
	case errors.Is(err, billing.ErrNotConfigured):
		writeErr(w, http.StatusNotImplemented, err.Error())
	case errors.Is(err, channels.ErrNotConfigured):
		writeErr(w, http.StatusConflict, err.Error())
	case errors.Is(err, ai.ErrUpstream):
		log.Printf("ai error: %v", err)
		writeErr(w, http.StatusBadGateway, "the AI provider failed or returned unusable output; try again")
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		writeErr(w, http.StatusGatewayTimeout, "request timed out")
	default:
		log.Printf("internal error: %v", err)
		writeErr(w, http.StatusInternalServerError, "internal error")
	}
}

const maxJSON = 1 << 20

// decode reads a JSON body, rejecting unknown fields and oversized payloads.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSON))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

func logf(format string, args ...any) { log.Printf(format, args...) }

// testRL allows a handful of connection tests per organization per minute.
func (a *API) testRL(org string) bool { return a.testLimiter.allow(org) }
