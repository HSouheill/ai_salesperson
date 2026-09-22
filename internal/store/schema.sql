-- Multi-tenant schema. Every tenant-owned table carries org_id and every
-- query filters on it. agents/prospects/messages keep their full document in
-- `data` and expose only the columns we filter on.

-- data holds the public org document; settings_enc holds the tenant's
-- credentials (SMTP/IMAP/WhatsApp/integrations), encrypted with SECRETS_KEY.
CREATE TABLE IF NOT EXISTS orgs (
    id            TEXT PRIMARY KEY,
    parent_id     TEXT NOT NULL DEFAULT '',
    slug          TEXT NOT NULL DEFAULT '',
    inbound_token TEXT NOT NULL DEFAULT '',
    name          TEXT NOT NULL,
    plan          TEXT NOT NULL DEFAULT 'trial',
    has_imap      BOOLEAN NOT NULL DEFAULT false,
    data          JSONB NOT NULL DEFAULT '{}',
    settings_enc  TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS orgs_slug_key ON orgs (slug) WHERE slug <> '';
CREATE UNIQUE INDEX IF NOT EXISTS orgs_inbound_token_key ON orgs (inbound_token) WHERE inbound_token <> '';
CREATE INDEX IF NOT EXISTS orgs_parent ON orgs (parent_id) WHERE parent_id <> '';

CREATE TABLE IF NOT EXISTS users (
    id            TEXT PRIMARY KEY,
    org_id        TEXT NOT NULL REFERENCES orgs(id),
    email         TEXT NOT NULL,
    password_hash TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS users_email_key ON users (lower(email));

-- ref_id: '' for agents, agent_id for prospects, prospect_id for messages.
-- state:  agent status / prospect stage / message status.
CREATE TABLE IF NOT EXISTS agents (
    id TEXT PRIMARY KEY, org_id TEXT NOT NULL REFERENCES orgs(id),
    ref_id TEXT NOT NULL DEFAULT '', state TEXT NOT NULL, data JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS prospects (
    id TEXT PRIMARY KEY, org_id TEXT NOT NULL REFERENCES orgs(id),
    ref_id TEXT NOT NULL DEFAULT '', state TEXT NOT NULL, data JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS messages (
    id TEXT PRIMARY KEY, org_id TEXT NOT NULL REFERENCES orgs(id),
    ref_id TEXT NOT NULL DEFAULT '', state TEXT NOT NULL, data JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS agents_org    ON agents    (org_id, created_at);
CREATE INDEX IF NOT EXISTS prospects_org ON prospects (org_id, ref_id, state);
CREATE INDEX IF NOT EXISTS messages_org  ON messages  (org_id, ref_id, state);

CREATE INDEX IF NOT EXISTS agents_active ON agents (state) WHERE state = 'active';
CREATE INDEX IF NOT EXISTS prospects_email ON prospects (org_id, lower(data->'contact'->>'email'));

CREATE TABLE IF NOT EXISTS api_keys (
    id         TEXT PRIMARY KEY,
    org_id     TEXT NOT NULL REFERENCES orgs(id),
    name       TEXT NOT NULL,
    prefix     TEXT NOT NULL,
    hash       TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS api_keys_org ON api_keys (org_id);
