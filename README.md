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
  organizations with their own branding, dashboard, agents and CRM data, and can step into a client's workspace.
- **AI gateway**: Anthropic and any OpenAI-compatible server, retry with backoff, optional automatic fallback.

## API

JSON under `/v1`. Auth: `Authorization: Bearer <token>` (login) or `Bearer aisp_…` (API key, Pro+). Highlights:

```
POST /auth/signup | /auth/login      GET /me  /plans  /public/branding?slug=
GET|PUT /settings   POST /settings/test   POST /settings/rotate-inbound-token
GET|POST /agents    GET|PUT /agents/{id}   POST /agents/{id}/brain|status|research|discover|autopilot/run|followups
POST|GET /agents/{id}/prospects (JSON or text/csv)        GET /prospects/{id}
POST /prospects/{id}/research|outreach|replies|meeting|convert|lost    PUT /prospects/{id}/deal
GET /messages?status=…   POST /messages/{id}/review        GET /dashboard   GET /jobs/{id}
GET|POST /clients  PUT /clients/{id}  POST /clients/{id}/login          (Enterprise agencies)
GET|POST /api-keys  DELETE /api-keys/{id}                                (Pro+)
POST /billing/checkout|portal                                            POST /billing/webhook (Stripe)
POST /inbound/email/{token}      GET|POST /inbound/whatsapp/{token}      (provider callbacks)
```

## Testing

```bash
go test -race ./...
TEST_DATABASE_URL=postgres://user:pass@localhost:5432/db go test ./internal/store ./internal/httpapi   # same suites on Postgres (tables are truncated!)
cd web && npx tsc --noEmit && npm run build
```

## Limits and honest caveats

- **Plan limits are placeholders** (`internal/domain/plans.go`): agents, prospects, daily sends. Tune them against real costs.
- **Verified locally** against fakes: SMTP, IMAP (in-memory server), WhatsApp, Stripe, HubSpot, Google Places, OpenStreetMap,
  Redis (miniredis). **Not yet run against the live services**: do a smoke test with real accounts before launch.
- **WhatsApp**: business-started chats need a Meta-approved template with one variable; customers must have opt-in.
- **Cold email/WhatsApp legality** (consent, CAN-SPAM, GDPR/PDPL, sender identification) is the customer's responsibility.
  The product supplies opt-out handling, a footer and an approval step, but is not legal advice.
- **Discovery**: OpenStreetMap coverage varies by country; Google Places has its own terms (customer's key). No scraping of
  social networks or other sites that forbid it.
- No estimated *time-zone-aware* sending windows; no email open/click tracking; single-user organizations (no team seats/roles);
  the auth token is kept in `localStorage` (move to an httpOnly cookie before real customers); rate limiting is per instance.
