package store

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hussein/ai-salesperson/internal/domain"
)

// Memory is a non-persistent Store for development and tests.
type Memory struct {
	mu        sync.RWMutex
	seq       int
	orgs      map[string]domain.Org
	apiKeys   map[string]domain.APIKey // by id
	users     map[string]domain.User   // by lowercase email
	agents    map[string]row[domain.Agent]
	prospects map[string]row[domain.Prospect]
	messages  map[string]row[domain.Message]
}

type row[T any] struct {
	org, ref, state string
	seq             int
	v               T
}

func NewMemory() *Memory {
	return &Memory{
		orgs:      map[string]domain.Org{},
		apiKeys:   map[string]domain.APIKey{},
		users:     map[string]domain.User{},
		agents:    map[string]row[domain.Agent]{},
		prospects: map[string]row[domain.Prospect]{},
		messages:  map[string]row[domain.Message]{},
	}
}

func (m *Memory) Close() {}

// Ping always succeeds: there is nothing external to be unreachable.
func (m *Memory) Ping(context.Context) error { return nil }

func cloneOrg(o domain.Org) domain.Org {
	b, _ := json.Marshal(o)
	var c domain.Org
	_ = json.Unmarshal(b, &c)
	sb, _ := json.Marshal(o.Settings)
	_ = json.Unmarshal(sb, &c.Settings)
	c.InboundToken = o.InboundToken
	return c
}

func (m *Memory) CreateOrg(_ context.Context, o domain.Org, u domain.User) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := strings.ToLower(u.Email)
	if _, ok := m.users[k]; ok {
		return ErrConflict
	}
	for _, x := range m.orgs {
		if o.Slug != "" && x.Slug == o.Slug {
			return ErrConflict
		}
	}
	m.orgs[o.ID] = cloneOrg(o)
	m.users[k] = u
	return nil
}

func (m *Memory) UserByEmail(_ context.Context, email string) (domain.User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	u, ok := m.users[strings.ToLower(email)]
	if !ok {
		return u, ErrNotFound
	}
	return u, nil
}

func (m *Memory) GetOrg(_ context.Context, id string) (domain.Org, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	o, ok := m.orgs[id]
	if !ok {
		return o, ErrNotFound
	}
	return cloneOrg(o), nil
}

func (m *Memory) UpdateOrg(_ context.Context, o domain.Org) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.orgs[o.ID]
	if !ok {
		return ErrNotFound
	}
	o.InboxCursor = cur.InboxCursor // owned by the poller, never overwritten by an org update
	for _, x := range m.orgs {
		if x.ID != o.ID && o.Slug != "" && x.Slug == o.Slug {
			return ErrConflict
		}
	}
	m.orgs[o.ID] = cloneOrg(o)
	return nil
}

func (m *Memory) SetInboxCursor(_ context.Context, orgID string, c domain.InboxCursor) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.orgs[orgID]
	if !ok {
		return ErrNotFound
	}
	o.InboxCursor = c
	m.orgs[orgID] = o
	return nil
}

func (m *Memory) orgsWhere(f func(domain.Org) bool) []domain.Org {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []domain.Org
	for _, o := range m.orgs {
		if f(o) {
			out = append(out, cloneOrg(o))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

func (m *Memory) ListChildOrgs(_ context.Context, parentID string) ([]domain.Org, error) {
	return m.orgsWhere(func(o domain.Org) bool { return parentID != "" && o.ParentID == parentID }), nil
}

func (m *Memory) OrgByInboundToken(_ context.Context, tok string) (domain.Org, error) {
	os := m.orgsWhere(func(o domain.Org) bool { return tok != "" && o.InboundToken == tok })
	if len(os) == 0 {
		return domain.Org{}, ErrNotFound
	}
	return os[0], nil
}

func (m *Memory) OrgBySlug(_ context.Context, slug string) (domain.Org, error) {
	os := m.orgsWhere(func(o domain.Org) bool { return slug != "" && o.Slug == slug })
	if len(os) == 0 {
		return domain.Org{}, ErrNotFound
	}
	return os[0], nil
}

func (m *Memory) ListInboxOrgs(_ context.Context) ([]domain.Org, error) {
	return m.orgsWhere(func(o domain.Org) bool { return o.Settings.Email != nil && o.Settings.Email.IMAP != nil }), nil
}

// orgEmails returns the lowercase emails of every user belonging to orgID.
func (m *Memory) orgEmails(orgID string) []string {
	var out []string
	for email, u := range m.users {
		if u.OrgID == orgID {
			out = append(out, email)
		}
	}
	return out
}

func (m *Memory) ListOrgs(_ context.Context, q string, limit, offset int) ([]domain.Org, error) {
	q = strings.ToLower(strings.TrimSpace(q))
	// orgsWhere holds the read lock for the whole scan, so reading m.users
	// directly inside the predicate (via orgEmails) without a second lock is safe.
	matches := m.orgsWhere(func(o domain.Org) bool {
		if o.ParentID != "" {
			return false // client orgs are managed by their agency, not the global admin list
		}
		if q == "" {
			return true
		}
		if strings.Contains(strings.ToLower(o.Name), q) || strings.Contains(strings.ToLower(o.ID), q) {
			return true
		}
		for _, e := range m.orgEmails(o.ID) {
			if strings.Contains(e, q) {
				return true
			}
		}
		return false
	})
	if offset >= len(matches) {
		return []domain.Org{}, nil
	}
	end := offset + limit
	if end > len(matches) {
		end = len(matches)
	}
	return matches[offset:end], nil
}

func (m *Memory) CountOrgs(_ context.Context) (int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	n := 0
	for _, o := range m.orgs {
		if o.ParentID == "" {
			n++
		}
	}
	return n, nil
}

func (m *Memory) PutAPIKey(_ context.Context, k domain.APIKey) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.apiKeys[k.ID] = k
	return nil
}

func (m *Memory) APIKeyByHash(_ context.Context, hash string) (domain.APIKey, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, k := range m.apiKeys {
		if k.Hash == hash {
			return k, nil
		}
	}
	return domain.APIKey{}, ErrNotFound
}

func (m *Memory) ListAPIKeys(_ context.Context, org string) ([]domain.APIKey, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := []domain.APIKey{}
	for _, k := range m.apiKeys {
		if k.OrgID == org {
			out = append(out, k)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (m *Memory) DeleteAPIKey(_ context.Context, org, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if k, ok := m.apiKeys[id]; !ok || k.OrgID != org {
		return ErrNotFound
	}
	delete(m.apiKeys, id)
	return nil
}

func (m *Memory) ListActiveAgents(_ context.Context) ([]domain.Agent, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []domain.Agent
	for _, r := range m.agents {
		if r.v.Status == domain.AgentActive {
			out = append(out, r.v)
		}
	}
	return out, nil
}

func put[T any](m *Memory, t map[string]row[T], id, org, ref, state string, v T) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := t[id]
	if ok && r.org != org {
		return ErrNotFound // never let one tenant overwrite another's row
	}
	if !ok {
		m.seq++
		r.seq = m.seq
	}
	r.org, r.ref, r.state, r.v = org, ref, state, v
	t[id] = r
	return nil
}

func get[T any](m *Memory, t map[string]row[T], org, id string) (T, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r, ok := t[id]
	if !ok || r.org != org {
		var z T
		return z, ErrNotFound
	}
	return r.v, nil
}

// list returns the org's rows matching ref/state ("" matches anything).
func list[T any](m *Memory, t map[string]row[T], org, ref, state string, newestFirst bool) []T {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var rows []row[T]
	for _, r := range t {
		if r.org == org && (ref == "" || r.ref == ref) && (state == "" || r.state == state) {
			rows = append(rows, r)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if newestFirst {
			return rows[i].seq > rows[j].seq
		}
		return rows[i].seq < rows[j].seq
	})
	out := make([]T, len(rows))
	for i, r := range rows {
		out[i] = r.v
	}
	return out
}

func (m *Memory) PutAgent(_ context.Context, a domain.Agent) error {
	return put(m, m.agents, a.ID, a.OrgID, "", string(a.Status), a)
}
func (m *Memory) GetAgent(_ context.Context, org, id string) (domain.Agent, error) {
	return get(m, m.agents, org, id)
}
func (m *Memory) ListAgents(_ context.Context, org string) ([]domain.Agent, error) {
	return list(m, m.agents, org, "", "", true), nil
}

func (m *Memory) PutProspect(_ context.Context, p domain.Prospect) error {
	return put(m, m.prospects, p.ID, p.OrgID, p.AgentID, string(p.Stage), p)
}
func (m *Memory) GetProspect(_ context.Context, org, id string) (domain.Prospect, error) {
	return get(m, m.prospects, org, id)
}
func (m *Memory) ListProspects(_ context.Context, org string, f ProspectFilter) ([]domain.Prospect, error) {
	return list(m, m.prospects, org, f.AgentID, string(f.Stage), true), nil
}

func (m *Memory) FindProspectByContact(_ context.Context, org, email, phone string) (domain.Prospect, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	var best *domain.Prospect
	bestExact := false
	for _, p := range list(m, m.prospects, org, "", "", true) {
		exact := (email != "" && strings.ToLower(p.Contact.Email) == email) ||
			(NormPhone(phone) != "" && NormPhone(p.Contact.Phone) == NormPhone(phone))
		if !exact && !SamePhone(p.Contact.Phone, phone) {
			continue
		}
		// An exact match beats a suffix match; otherwise the most recently active prospect wins.
		if best == nil || (exact && !bestExact) || (exact == bestExact && p.UpdatedAt.After(best.UpdatedAt)) {
			p := p
			best, bestExact = &p, exact
		}
	}
	if best == nil {
		return domain.Prospect{}, ErrNotFound
	}
	return *best, nil
}

func (m *Memory) CountSent(_ context.Context, org, agentID string, since time.Time) (int, error) {
	n := 0
	for _, msg := range list(m, m.messages, org, "", string(domain.MsgSent), true) {
		if msg.Direction == domain.Outbound && (agentID == "" || msg.AgentID == agentID) && msg.SentAt != nil && !msg.SentAt.Before(since) {
			n++
		}
	}
	return n, nil
}

func (m *Memory) PutMessage(_ context.Context, msg domain.Message) error {
	return put(m, m.messages, msg.ID, msg.OrgID, msg.ProspectID, string(msg.Status), msg)
}
func (m *Memory) GetMessage(_ context.Context, org, id string) (domain.Message, error) {
	return get(m, m.messages, org, id)
}
func (m *Memory) ListMessages(_ context.Context, org string, f MessageFilter) ([]domain.Message, error) {
	return list(m, m.messages, org, f.ProspectID, string(f.Status), false), nil
}

func (m *Memory) Counts(_ context.Context, org string) (Counts, error) {
	c := Counts{ByStage: map[domain.Stage]int{}, Messages: map[string]int{}}
	for _, p := range list(m, m.prospects, org, "", "", true) {
		c.ByStage[p.Stage]++
		if p.Research != nil {
			c.Researched++
		}
		if p.MeetingAt != nil {
			c.Meetings++
		}
		switch p.Stage {
		case domain.StageQualified, domain.StageMeetingBooked:
			c.Pipeline += p.DealValue
		case domain.StageWon:
			c.WonValue += p.DealValue
		}
	}
	for _, msg := range list(m, m.messages, org, "", "", true) {
		c.Messages[string(msg.Direction)+"/"+string(msg.Status)]++
	}
	return c, nil
}

// Digits keeps only the digits of a phone number, for matching across formats.
func Digits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// NormPhone keeps the digits of a phone number and drops leading zeros (the
// "00" international prefix and the local trunk "0"), so "03 123 456",
// "+961 3 123 456" and "00961 3 123 456" all compare on their significant digits.
func NormPhone(s string) string { return strings.TrimLeft(Digits(s), "0") }

// SamePhone reports whether two numbers match on their last 9 significant
// digits (at least 7 required), tolerating a missing country code.
func SamePhone(a, b string) bool {
	na, nb := NormPhone(a), NormPhone(b)
	if len(na) < 7 || len(nb) < 7 {
		return false
	}
	n := min(9, len(na), len(nb))
	return na[len(na)-n:] == nb[len(nb)-n:]
}
