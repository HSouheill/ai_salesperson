// Package store defines persistence for the platform. Every query is scoped by
// organization ID, which is how multi-tenant isolation is enforced.
package store

import (
	"context"
	"errors"
	"time"

	"github.com/hussein/ai-salesperson/internal/domain"
)

var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("already exists")
)

type ProspectFilter struct {
	AgentID string
	Stage   domain.Stage
}

type MessageFilter struct {
	ProspectID string
	Status     domain.MessageStatus
}

type Store interface {
	// CreateOrg creates an organization together with its first user.
	CreateOrg(ctx context.Context, o domain.Org, u domain.User) error
	UserByEmail(ctx context.Context, email string) (domain.User, error)
	GetOrg(ctx context.Context, id string) (domain.Org, error)
	UpdateOrg(ctx context.Context, o domain.Org) error
	// SetInboxCursor updates only the mailbox cursor, so polling never
	// overwrites settings a user is editing at the same time.
	SetInboxCursor(ctx context.Context, orgID string, c domain.InboxCursor) error
	ListChildOrgs(ctx context.Context, parentID string) ([]domain.Org, error)
	OrgByInboundToken(ctx context.Context, token string) (domain.Org, error)
	OrgBySlug(ctx context.Context, slug string) (domain.Org, error)
	// ListInboxOrgs returns orgs that have an IMAP mailbox configured.
	ListInboxOrgs(ctx context.Context) ([]domain.Org, error)

	PutAPIKey(ctx context.Context, k domain.APIKey) error
	APIKeyByHash(ctx context.Context, hash string) (domain.APIKey, error)
	ListAPIKeys(ctx context.Context, orgID string) ([]domain.APIKey, error)
	DeleteAPIKey(ctx context.Context, orgID, id string) error

	// ListActiveAgents spans all organizations; it exists for the scheduler.
	ListActiveAgents(ctx context.Context) ([]domain.Agent, error)

	PutAgent(ctx context.Context, a domain.Agent) error
	GetAgent(ctx context.Context, orgID, id string) (domain.Agent, error)
	ListAgents(ctx context.Context, orgID string) ([]domain.Agent, error)

	PutProspect(ctx context.Context, p domain.Prospect) error
	GetProspect(ctx context.Context, orgID, id string) (domain.Prospect, error)
	ListProspects(ctx context.Context, orgID string, f ProspectFilter) ([]domain.Prospect, error)
	// FindProspectByContact matches an inbound sender to a prospect by email
	// (case-insensitive) or phone (digits only). Empty arguments are ignored.
	FindProspectByContact(ctx context.Context, orgID, email, phoneDigits string) (domain.Prospect, error)

	PutMessage(ctx context.Context, m domain.Message) error
	GetMessage(ctx context.Context, orgID, id string) (domain.Message, error)
	ListMessages(ctx context.Context, orgID string, f MessageFilter) ([]domain.Message, error)

	// CountSent counts outbound messages sent since t (agentID "" = all agents).
	CountSent(ctx context.Context, orgID, agentID string, since time.Time) (int, error)
	Counts(ctx context.Context, orgID string) (Counts, error)
	Close()
}

// Counts are the raw aggregates the dashboard is computed from.
type Counts struct {
	ByStage    map[domain.Stage]int
	Researched int
	Meetings   int     // prospects that ever had a meeting booked
	Pipeline   float64 // deal value of qualified + meeting-booked prospects
	WonValue   float64
	// Messages is keyed "<direction>/<status>", e.g. "outbound/sent".
	Messages map[string]int
}

func BuildStats(c Counts) domain.Stats {
	s := domain.Stats{ByStage: c.ByStage, ProspectsResearched: c.Researched}
	for _, n := range c.ByStage {
		s.ProspectsIdentified += n
	}
	s.ActiveConversations = c.ByStage[domain.StageEngaged] + c.ByStage[domain.StageQualified]
	s.MeetingsBooked = c.Meetings
	s.Won = c.ByStage[domain.StageWon]
	s.QualifiedLeads = c.ByStage[domain.StageQualified] + c.ByStage[domain.StageMeetingBooked] + s.Won
	s.EstimatedPipeline, s.WonRevenue = c.Pipeline, c.WonValue
	for k, n := range c.Messages {
		switch k {
		case "outbound/sent":
			s.OutreachSent += n
			s.OutreachPrepared += n
		case "outbound/pending_approval":
			s.AwaitingApproval += n
			s.OutreachPrepared += n
		case "outbound/approved", "outbound/failed":
			s.OutreachPrepared += n
		}
	}
	return s
}

var (
	_ Store = (*Memory)(nil)
	_ Store = (*Postgres)(nil)
)
