# AI Salesperson

An AI sales workforce, as described in `AI_Salesperson_script`:
**Find → Research → Personalize → Engage → Qualify → Follow Up → Book Meeting → Convert.**

Go API (`cmd/server`, `internal/`) + Next.js dashboard (`web/`). Multi-tenant: every query is scoped by organization.

## Quick start (development)

```bash
export JWT_SECRET=$(openssl rand -hex 32)
export ANTHROPIC_API_KEY=...            # or OPENAI_API_KEY; omit for the offline mock AI
DEV_LOG_CHANNELS=true go run ./cmd/server          # in-memory store + in-process queue, :8080
cd web && npm install && npm run dev               # dashboard on :3000
```

`DEV_LOG_CHANNELS=true` makes unconnected channels "deliver" by logging. **Without it, nothing is sent until the
customer connects their own email/WhatsApp in Settings** — that is the intended behaviour.

## Production

```bash
export JWT_SECRET=$(openssl rand -hex 32) SECRETS_KEY=$(openssl rand -hex 32)   # keep SECRETS_KEY safe
docker compose up --build        # API + Postgres + Redis
```

See `.env.example` for every setting. `SECRETS_KEY` and a real `DATABASE_URL` are mandatory together; the server
refuses to start otherwise. Run behind HTTPS; set `PUBLIC_URL`, `WEB_URL`, `CORS_ORIGIN` and (behind a proxy) `TRUST_PROXY=true`.

Operator command (Enterprise is sold by contact, so it is granted by hand):
```bash
DATABASE_URL=… SECRETS_KEY=… go run ./cmd/server set-plan owner@agency.com enterprise
```

## How it works

- **Customers connect their own accounts** (Settings): SMTP to send, IMAP (read-only, cursor-based; it never touches
  other mail) or an inbound webhook to receive replies, and/or their own WhatsApp Business number. Credentials are
  encrypted at rest (AES-256-GCM) and never returned by the API.
- **Find**: customer CSV, OpenStreetMap (free), or Google Places (the customer's own key). Public contact emails are
  read from a business's own site (robots.txt honoured, SSRF-guarded).
- **Research & score**: the AI reads the supplied facts plus the prospect's public website and explains each score.
- **Engage**: personalized outreach → **human approval by default** (auto-send needs the Pro plan) → sent through the
  customer's channel with an opt-out line and postal address. Daily send limits per agent and plan.
- **Qualify / follow up**: replies are classified (question, objection, meeting request, opt-out…), answered, and
  follow-ups are drafted on the agent's sequence, threaded in the same email conversation.
- **Autopilot**: a scheduler (Redis-deduplicated across instances) advances every active autopilot agent: discover →
  research → outreach → follow-ups, bounded per tick by the agent's min-score and daily limit.
- **Hand-off**: qualified leads alert the customer's sales team by email and are pushed to their CRM (signed webhook,
  HubSpot); meetings get a confirmation; deals convert with real value → dashboard pipeline and revenue.
- **Compliance guards**: opt-outs are detected deterministically (no AI needed), are permanent and org-wide (a person
  who said stop is suppressed even if a different agent finds them again), cancel pending drafts, and are re-checked at
  send time. Lapsed accounts stop paid work but still honour opt-outs.
- **Billing**: Stripe Checkout/Portal + signed webhooks; 14-day trial. **White-label**: Enterprise agencies create client
  organizations with their own branding, dashboard, agents and CRM data, and can step into a client's workspace purely
  via cookies (no token ever touches the agency's own page JS — see Security below).
- **AI gateway**: Anthropic and any OpenAI-compatible server, retry with backoff, optional automatic fallback.
- **One-click unsubscribe** (RFC 8058): outbound email carries `List-Unsubscribe`/`List-Unsubscribe-Post`, which Gmail
  and Yahoo require of bulk senders or they throttle/junk the mail. The link is a signed, auth-free, per-prospect token.

## Security

- **Dashboard sessions are httpOnly cookies**, not a token the page's JS can read — an XSS bug can't exfiltrate a
  session. A separate, non-httpOnly `aisp_csrf` cookie is double-submitted as the `X-CSRF-Token` header on every
  mutating request; a cookie-authenticated request without a matching header is refused (403). Non-browser clients
  (API keys, `curl`, scripts) keep using `Authorization: Bearer …` exactly as before and are exempt from the CSRF
  check — a cross-site page cannot set that header without already running script on this origin.
- **Rate limiting is Redis-backed when `REDIS_URL` is set**, so the budget holds across every instance; it falls back
  to a single-instance in-process limiter otherwise (`internal/ratelimit`).
- **Error tracking**: set `SENTRY_DSN` to report internal-server errors and recovered panics. A panic in a handler now
  returns a clean 500 instead of a torn connection.
- **Operator admin API** (`/v1/admin/*`, `ADMIN_TOKEN`-gated, closed by default): list/search organizations, suspend
  one immediately (independent of its plan or billing status), and a dependency-health view.
- **Signup abuse protection (optional)**: set `TURNSTILE_SECRET_KEY` (server) and `NEXT_PUBLIC_TURNSTILE_SITE_KEY`
  (dashboard) to require a Cloudflare Turnstile check on signup. Off by default — no widget, no token required — until
  both are configured.

## API

JSON under `/v1`. Auth: `Authorization: Bearer <token>` (login) or `Bearer aisp_…` (API key, Pro+). Highlights:

```
POST /auth/signup | /auth/login | /auth/logout      GET /me  /plans  /public/branding?slug=
GET|PUT /settings   POST /settings/test   POST /settings/rotate-inbound-token
GET|POST /agents    GET|PUT /agents/{id}   POST /agents/{id}/brain|status|research|discover|autopilot/run|followups
POST|GET /agents/{id}/prospects (JSON or text/csv)        GET /prospects/{id}
POST /prospects/{id}/research|outreach|replies|meeting|convert|lost    PUT /prospects/{id}/deal
GET /messages?status=…   POST /messages/{id}/review        GET /dashboard   GET /jobs/{id}
GET|POST /clients  PUT /clients/{id}  POST /clients/{id}/login  POST /clients/exit    (Enterprise agencies)
GET|POST /api-keys  DELETE /api-keys/{id}                                (Pro+)
POST /billing/checkout|portal                                            POST /billing/webhook (Stripe)
POST /inbound/email/{token}      GET|POST /inbound/whatsapp/{token}      (provider callbacks)
GET|POST /unsubscribe/{org}/{prospect}/{token}                           (one-click unsubscribe, no auth)
GET /v1/admin/orgs  POST /v1/admin/orgs/{id}/suspend|unsuspend  GET /v1/admin/health   (ADMIN_TOKEN, operator only)
```

Mutating requests authenticated by the dashboard's session cookie must also send the `X-CSRF-Token` header (see
Security above); requests authenticated by `Authorization: Bearer …` do not need it.

## Testing

```bash
go test -race ./...
TEST_DATABASE_URL=postgres://user:pass@localhost:5432/db go test ./internal/store ./internal/httpapi   # same suites on Postgres (tables are truncated!)
cd web && npx tsc --noEmit && npm run build
```

`.github/workflows/ci.yml` runs the Go suite (with a Postgres service container) plus `govulncheck`, and the web
typecheck/build/`npm audit`, on every push and PR.

### Backups

```bash
DATABASE_URL=… ./scripts/backup.sh [dir]            # timestamped, gzip-compressed pg_dump; optional S3 upload
DATABASE_URL=… ./scripts/restore.sh backup.sql.gz    # replaces the target database; asks for confirmation
```

Use `psql`/`pg_dump` matching the server's major version (e.g. the same `postgres:16-alpine` image used to run it) —
`restore.sh` warns if your client tools are newer, since a newer `pg_dump` can emit session settings an older server
doesn't recognize. Verified end-to-end against a real database (seed → backup → wipe → restore → data intact), and
the version-mismatch case above is exactly what that testing caught.

`SECRETS_KEY` is what makes a restored database's stored customer credentials readable — back it up separately
(a password manager or secrets vault, never alongside the dump) and keep it forever.

### Load test

`GET /v1/dashboard`, 200 concurrent connections, 20s, real Postgres + Redis, mock AI, one seeded org/agent/50
prospects: **9,544 req/s**, p50 20ms, p99 25ms, zero errors. A write path (`POST /prospects/{id}/research`, same row,
50 concurrent, 15s): **2,253 req/s**, p50 20ms, p99 42ms, zero errors. The login rate limiter was confirmed to hold
under real concurrent load (30 concurrent, 5s): 10 requests succeeded, 50,834 were correctly rejected with 429. This
characterizes the HTTP/DB/queue layers only — real AI provider latency and cost are not reflected (mock AI responds
near-instantly); budget for real model latency separately once you're sending live traffic.

## Limits and honest caveats

- **Plan limits are placeholders** (`internal/domain/plans.go`): agents, prospects, daily sends. Tune them against real costs.
- **Verified locally** against fakes: SMTP, IMAP (in-memory server), WhatsApp, Stripe, HubSpot, Google Places, OpenStreetMap,
  Redis (miniredis). **Not yet run against the live services**: do a smoke test with real accounts before launch.
- **WhatsApp**: business-started chats need a Meta-approved template with one variable; customers must have opt-in.
- **Cold email/WhatsApp legality** (consent, CAN-SPAM, GDPR/PDPL, sender identification) is the customer's responsibility.
  The product supplies opt-out handling, a footer and an approval step, but is not legal advice.
- **Discovery**: OpenStreetMap coverage varies by country; Google Places has its own terms (customer's key). No scraping of
  social networks or other sites that forbid it.
- **Email verification is tracked but not enforced**: `Org.EmailVerifiedAt` exists and nothing sets or checks it yet —
  a signup can use any syntactically valid address today. Enforcing it needs the platform's own transactional-email
  sending (distinct from a customer's connected SMTP, which only sends *their* outreach) and a decision on what to
  block before verification; neither is built.
- **No sending-reputation monitoring**: this is an operational practice once real mail is flowing (Google Postmaster
  Tools, Microsoft SNDS), not something a codebase can supply in advance.
- **No CRM-as-a-source** (pulling prospects *from* a customer's CRM — only pushing qualified leads *to* one is built),
  and **no social-DM channels** (LinkedIn/Instagram) — both need a developer-app registration/approval with that
  provider, which only the account owner can obtain, before either is worth building.
- No estimated *time-zone-aware* sending windows; no email open/click tracking; single-user organizations (no team
  seats/roles); the dashboard is poll-based, not real-time (SSE/WebSocket push is a reasonable next step).
