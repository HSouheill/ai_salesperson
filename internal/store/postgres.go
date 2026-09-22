package store

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hussein/ai-salesperson/internal/domain"
	"github.com/hussein/ai-salesperson/internal/secrets"
)

//go:embed schema.sql
var schema string

type Postgres struct {
	pool *pgxpool.Pool
	box  *secrets.Box // encrypts tenant credentials at rest
}

func NewPostgres(ctx context.Context, url string, box *secrets.Box) (*Postgres, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	if _, err := pool.Exec(ctx, schema); err != nil {
		pool.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &Postgres{pool: pool, box: box}, nil
}

func (p *Postgres) Close() { p.pool.Close() }

func isUnique(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23505"
}

func (p *Postgres) orgArgs(o domain.Org) ([]any, error) {
	doc, err := json.Marshal(o)
	if err != nil {
		return nil, err
	}
	sb, err := json.Marshal(o.Settings)
	if err != nil {
		return nil, err
	}
	enc, err := p.box.Seal(sb)
	if err != nil {
		return nil, err
	}
	imap := o.Settings.Email != nil && o.Settings.Email.IMAP != nil
	return []any{o.ID, o.ParentID, o.Slug, o.InboundToken, o.Name, string(o.Plan), imap, doc, enc}, nil
}

func (p *Postgres) CreateOrg(ctx context.Context, o domain.Org, u domain.User) error {
	args, err := p.orgArgs(o)
	if err != nil {
		return err
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `INSERT INTO orgs (id, parent_id, slug, inbound_token, name, plan, has_imap, data, settings_enc)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, args...); err != nil {
		if isUnique(err) {
			return ErrConflict
		}
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO users (id, org_id, email, password_hash) VALUES ($1,$2,$3,$4)`,
		u.ID, u.OrgID, u.Email, u.PasswordHash); err != nil {
		if isUnique(err) {
			return ErrConflict
		}
		return err
	}
	return tx.Commit(ctx)
}

func (p *Postgres) UpdateOrg(ctx context.Context, o domain.Org) error {
	args, err := p.orgArgs(o)
	if err != nil {
		return err
	}
	tag, err := p.pool.Exec(ctx, `UPDATE orgs SET parent_id=$2, slug=$3, inbound_token=$4, name=$5, plan=$6,
		has_imap=$7, settings_enc=$9,
		data = jsonb_set($8::jsonb, '{inbox_cursor}', coalesce(orgs.data->'inbox_cursor', '{}'::jsonb)) WHERE id=$1`, args...)
	if err != nil {
		if isUnique(err) {
			return ErrConflict
		}
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

const orgCols = `SELECT data, inbound_token, settings_enc FROM orgs`

func (p *Postgres) scanOrg(data []byte, token, enc string) (domain.Org, error) {
	var o domain.Org
	if err := json.Unmarshal(data, &o); err != nil {
		return o, err
	}
	o.InboundToken = token
	if enc != "" {
		plain, err := p.box.Open(enc)
		if err != nil {
			return o, fmt.Errorf("org %s settings: %w", o.ID, err)
		}
		if err := json.Unmarshal(plain, &o.Settings); err != nil {
			return o, err
		}
	}
	return o, nil
}

func (p *Postgres) orgsQuery(ctx context.Context, where string, args ...any) ([]domain.Org, error) {
	rows, err := p.pool.Query(ctx, orgCols+" WHERE "+where+" ORDER BY created_at, id", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Org
	for rows.Next() {
		var data []byte
		var tok, enc string
		if err := rows.Scan(&data, &tok, &enc); err != nil {
			return nil, err
		}
		o, err := p.scanOrg(data, tok, enc)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (p *Postgres) orgOne(ctx context.Context, where string, arg string) (domain.Org, error) {
	os, err := p.orgsQuery(ctx, where, arg)
	if err != nil {
		return domain.Org{}, err
	}
	if len(os) == 0 {
		return domain.Org{}, ErrNotFound
	}
	return os[0], nil
}

func (p *Postgres) GetOrg(ctx context.Context, id string) (domain.Org, error) {
	return p.orgOne(ctx, "id=$1", id)
}
func (p *Postgres) OrgByInboundToken(ctx context.Context, t string) (domain.Org, error) {
	if t == "" {
		return domain.Org{}, ErrNotFound
	}
	return p.orgOne(ctx, "inbound_token=$1", t)
}
func (p *Postgres) OrgBySlug(ctx context.Context, s string) (domain.Org, error) {
	if s == "" {
		return domain.Org{}, ErrNotFound
	}
	return p.orgOne(ctx, "slug=$1", s)
}
func (p *Postgres) SetInboxCursor(ctx context.Context, orgID string, c domain.InboxCursor) error {
	b, _ := json.Marshal(c)
	tag, err := p.pool.Exec(ctx, `UPDATE orgs SET data = jsonb_set(data, '{inbox_cursor}', $2::jsonb) WHERE id=$1`, orgID, string(b))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *Postgres) ListChildOrgs(ctx context.Context, parent string) ([]domain.Org, error) {
	if parent == "" {
		return nil, nil
	}
	return p.orgsQuery(ctx, "parent_id=$1", parent)
}
func (p *Postgres) ListInboxOrgs(ctx context.Context) ([]domain.Org, error) {
	return p.orgsQuery(ctx, "has_imap")
}

func (p *Postgres) PutAPIKey(ctx context.Context, k domain.APIKey) error {
	_, err := p.pool.Exec(ctx, `INSERT INTO api_keys (id, org_id, name, prefix, hash) VALUES ($1,$2,$3,$4,$5)`,
		k.ID, k.OrgID, k.Name, k.Prefix, k.Hash)
	return err
}
func (p *Postgres) APIKeyByHash(ctx context.Context, hash string) (domain.APIKey, error) {
	var k domain.APIKey
	err := p.pool.QueryRow(ctx, `SELECT id, org_id, name, prefix, hash, created_at FROM api_keys WHERE hash=$1`, hash).
		Scan(&k.ID, &k.OrgID, &k.Name, &k.Prefix, &k.Hash, &k.CreatedAt)
	return k, mapErr(err)
}
func (p *Postgres) ListAPIKeys(ctx context.Context, org string) ([]domain.APIKey, error) {
	rows, err := p.pool.Query(ctx, `SELECT id, org_id, name, prefix, hash, created_at FROM api_keys WHERE org_id=$1 ORDER BY created_at`, org)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.APIKey{}
	for rows.Next() {
		var k domain.APIKey
		if err := rows.Scan(&k.ID, &k.OrgID, &k.Name, &k.Prefix, &k.Hash, &k.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}
func (p *Postgres) DeleteAPIKey(ctx context.Context, org, id string) error {
	tag, err := p.pool.Exec(ctx, `DELETE FROM api_keys WHERE org_id=$1 AND id=$2`, org, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *Postgres) ListActiveAgents(ctx context.Context) ([]domain.Agent, error) {
	rows, err := p.pool.Query(ctx, `SELECT data FROM agents WHERE state='active' ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Agent
	for rows.Next() {
		var data []byte
		var a domain.Agent
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(data, &a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (p *Postgres) FindProspectByContact(ctx context.Context, org, email, phone string) (domain.Prospect, error) {
	phone = NormPhone(phone) // same normalization as the SQL below and as the in-memory store
	const norm = `ltrim(regexp_replace(coalesce(data->'contact'->>'phone',''), '\D', '', 'g'), '0')`
	var data []byte
	err := p.pool.QueryRow(ctx, `SELECT data FROM prospects WHERE org_id=$1 AND (
		($2 <> '' AND lower(data->'contact'->>'email') = lower($2)) OR
		(length($3) >= 7 AND length(`+norm+`) >= 7 AND
		 right(`+norm+`, least(9, length(`+norm+`), length($3))) = right($3, least(9, length(`+norm+`), length($3)))))
		ORDER BY (lower(data->'contact'->>'email') = lower($2) OR `+norm+` = $3) DESC, created_at DESC LIMIT 1`,
		org, strings.TrimSpace(email), phone).Scan(&data)
	if err != nil {
		return domain.Prospect{}, mapErr(err)
	}
	var pr domain.Prospect
	return pr, json.Unmarshal(data, &pr)
}

func (p *Postgres) CountSent(ctx context.Context, org, agentID string, since time.Time) (int, error) {
	var n int
	err := p.pool.QueryRow(ctx, `SELECT count(*) FROM messages WHERE org_id=$1 AND state='sent'
		AND data->>'direction'='outbound' AND ($2='' OR data->>'agent_id'=$2)
		AND (data->>'sent_at')::timestamptz >= $3`, org, agentID, since).Scan(&n)
	return n, err
}

func (p *Postgres) UserByEmail(ctx context.Context, email string) (domain.User, error) {
	var u domain.User
	err := p.pool.QueryRow(ctx,
		`SELECT id, org_id, email, password_hash, created_at FROM users WHERE lower(email)=lower($1)`, email).
		Scan(&u.ID, &u.OrgID, &u.Email, &u.PasswordHash, &u.CreatedAt)
	return u, mapErr(err)
}

func mapErr(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// table is a whitelist of table names, so they can be safely formatted into SQL.
type table string

const (
	tAgents    table = "agents"
	tProspects table = "prospects"
	tMessages  table = "messages"
)

func (p *Postgres) put(ctx context.Context, t table, id, org, ref, state string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	tag, err := p.pool.Exec(ctx, fmt.Sprintf(`
		INSERT INTO %[1]s AS t (id, org_id, ref_id, state, data) VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (id) DO UPDATE SET ref_id=EXCLUDED.ref_id, state=EXCLUDED.state, data=EXCLUDED.data
		WHERE t.org_id = EXCLUDED.org_id`, t), id, org, ref, state, data)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound // id exists under another org
	}
	return nil
}

func getOne[T any](ctx context.Context, p *Postgres, t table, org, id string) (T, error) {
	var v T
	var data []byte
	err := p.pool.QueryRow(ctx, fmt.Sprintf(`SELECT data FROM %s WHERE org_id=$1 AND id=$2`, t), org, id).Scan(&data)
	if err != nil {
		return v, mapErr(err)
	}
	return v, json.Unmarshal(data, &v)
}

func listAll[T any](ctx context.Context, p *Postgres, t table, org, ref, state string, order string) ([]T, error) {
	rows, err := p.pool.Query(ctx, fmt.Sprintf(`
		SELECT data FROM %s
		WHERE org_id=$1 AND ($2='' OR ref_id=$2) AND ($3='' OR state=$3)
		ORDER BY created_at %s, id`, t, order), org, ref, state)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []T{}
	for rows.Next() {
		var data []byte
		var v T
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(data, &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (p *Postgres) PutAgent(ctx context.Context, a domain.Agent) error {
	return p.put(ctx, tAgents, a.ID, a.OrgID, "", string(a.Status), a)
}
func (p *Postgres) GetAgent(ctx context.Context, org, id string) (domain.Agent, error) {
	return getOne[domain.Agent](ctx, p, tAgents, org, id)
}
func (p *Postgres) ListAgents(ctx context.Context, org string) ([]domain.Agent, error) {
	return listAll[domain.Agent](ctx, p, tAgents, org, "", "", "DESC")
}

func (p *Postgres) PutProspect(ctx context.Context, x domain.Prospect) error {
	return p.put(ctx, tProspects, x.ID, x.OrgID, x.AgentID, string(x.Stage), x)
}
func (p *Postgres) GetProspect(ctx context.Context, org, id string) (domain.Prospect, error) {
	return getOne[domain.Prospect](ctx, p, tProspects, org, id)
}
func (p *Postgres) ListProspects(ctx context.Context, org string, f ProspectFilter) ([]domain.Prospect, error) {
	return listAll[domain.Prospect](ctx, p, tProspects, org, f.AgentID, string(f.Stage), "DESC")
}

func (p *Postgres) PutMessage(ctx context.Context, m domain.Message) error {
	return p.put(ctx, tMessages, m.ID, m.OrgID, m.ProspectID, string(m.Status), m)
}
func (p *Postgres) GetMessage(ctx context.Context, org, id string) (domain.Message, error) {
	return getOne[domain.Message](ctx, p, tMessages, org, id)
}
func (p *Postgres) ListMessages(ctx context.Context, org string, f MessageFilter) ([]domain.Message, error) {
	return listAll[domain.Message](ctx, p, tMessages, org, f.ProspectID, string(f.Status), "ASC")
}

func (p *Postgres) Counts(ctx context.Context, org string) (Counts, error) {
	c := Counts{ByStage: map[domain.Stage]int{}, Messages: map[string]int{}}
	rows, err := p.pool.Query(ctx, `SELECT state, count(*) FROM prospects WHERE org_id=$1 GROUP BY state`, org)
	if err != nil {
		return c, err
	}
	for rows.Next() {
		var s string
		var n int
		if err := rows.Scan(&s, &n); err != nil {
			rows.Close()
			return c, err
		}
		c.ByStage[domain.Stage(s)] = n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return c, err
	}
	if err := p.pool.QueryRow(ctx,
		`SELECT count(*) FILTER (WHERE data ? 'research'), count(*) FILTER (WHERE data ? 'meeting_at'),
			coalesce(sum((data->>'deal_value')::numeric) FILTER (WHERE state IN ('qualified','meeting_booked')), 0)::float8,
			coalesce(sum((data->>'deal_value')::numeric) FILTER (WHERE state = 'won'), 0)::float8
		 FROM prospects WHERE org_id=$1`, org).Scan(&c.Researched, &c.Meetings, &c.Pipeline, &c.WonValue); err != nil {
		return c, err
	}
	rows, err = p.pool.Query(ctx,
		`SELECT data->>'direction', state, count(*) FROM messages WHERE org_id=$1 GROUP BY 1,2`, org)
	if err != nil {
		return c, err
	}
	defer rows.Close()
	for rows.Next() {
		var d, s string
		var n int
		if err := rows.Scan(&d, &s, &n); err != nil {
			return c, err
		}
		c.Messages[d+"/"+s] = n
	}
	return c, rows.Err()
}
