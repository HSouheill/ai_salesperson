// Package domain holds the core types of the AI Salesperson platform.
package domain

import "time"

type Plan string

const (
	PlanTrial      Plan = "trial"
	PlanStarter    Plan = "starter"
	PlanGrowth     Plan = "growth"
	PlanPro        Plan = "pro"
	PlanEnterprise Plan = "enterprise"
)

type Billing struct {
	CustomerID     string `json:"customer_id,omitempty"`
	SubscriptionID string `json:"subscription_id,omitempty"`
	Status         string `json:"status,omitempty"` // Stripe subscription status; empty for manually assigned plans
}

// Branding is what a white-label agency's clients see instead of our own brand.
type Branding struct {
	ProductName  string `json:"product_name,omitempty"`
	LogoURL      string `json:"logo_url,omitempty"`
	PrimaryColor string `json:"primary_color,omitempty"` // #rrggbb
}

type Org struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Plan        Plan       `json:"plan"`
	ParentID    string     `json:"parent_id,omitempty"` // set for an agency's client organizations
	Slug        string     `json:"slug,omitempty"`      // public handle used for branded login pages
	TrialEndsAt *time.Time `json:"trial_ends_at,omitempty"`
	Billing     Billing    `json:"billing"`
	Branding    Branding   `json:"branding"`
	CreatedAt   time.Time  `json:"created_at"`
	// InboxCursor remembers how far the customer's mailbox has been read, so
	// old mail is never answered and their mail is never marked as read.
	InboxCursor InboxCursor `json:"inbox_cursor"`

	// Never serialized into API responses; the store persists them separately
	// (Settings encrypted at rest).
	InboundToken string      `json:"-"`
	Settings     OrgSettings `json:"-"`
}

type InboxCursor struct {
	UIDValidity uint32 `json:"uid_validity"`
	LastUID     uint32 `json:"last_uid"`
}

// Entitled reports whether the org may send messages and run automation.
// Client orgs are additionally gated by their agency (checked by the caller).
func (o Org) Entitled(now time.Time) bool {
	if o.Plan == PlanTrial {
		return o.TrialEndsAt != nil && now.Before(*o.TrialEndsAt)
	}
	switch o.Billing.Status {
	case "canceled", "unpaid", "past_due", "incomplete_expired":
		return false
	}
	return true
}

type User struct {
	ID           string    `json:"id"`
	OrgID        string    `json:"org_id"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
}

// BusinessProfile describes what the customer sells.
type BusinessProfile struct {
	Website      string   `json:"website,omitempty"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Products     []string `json:"products,omitempty"`
	Services     []string `json:"services,omitempty"`
	TargetMarket string   `json:"target_market,omitempty"`
	ValueProp    string   `json:"value_proposition,omitempty"`
	FAQs         []FAQ    `json:"faqs,omitempty"`
}

type FAQ struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

// Targeting is who the agent should sell to.
type Targeting struct {
	Industries   []string `json:"industries"`
	Locations    []string `json:"locations"`
	CompanySizes []string `json:"company_sizes,omitempty"`
	Goals        string   `json:"goals,omitempty"`
}

type Objection struct {
	Objection string `json:"objection"`
	Response  string `json:"response"`
}

type FollowUp struct {
	AfterDays int    `json:"after_days"`
	Angle     string `json:"angle"`
}

// Brain is the generated sales strategy ("AI Sales Brain").
type Brain struct {
	IdealCustomerProfile   string      `json:"ideal_customer_profile"`
	Strategy               string      `json:"strategy"`
	TargetIndustries       []string    `json:"target_industries"`
	PainPoints             []string    `json:"pain_points"`
	QualificationQuestions []string    `json:"qualification_questions"`
	OutreachGuidance       string      `json:"outreach_guidance"`
	FollowUpSequence       []FollowUp  `json:"follow_up_sequence"`
	ObjectionHandling      []Objection `json:"objection_handling"`
	ClosingStrategies      []string    `json:"closing_strategies"`
	GeneratedAt            time.Time   `json:"generated_at"`
}

type AgentStatus string

const (
	AgentDraft  AgentStatus = "draft"
	AgentActive AgentStatus = "active"
	AgentPaused AgentStatus = "paused"
)

type Agent struct {
	ID        string          `json:"id"`
	OrgID     string          `json:"org_id"`
	Name      string          `json:"name"`
	Persona   string          `json:"persona,omitempty"`
	Channel   string          `json:"channel"`
	Profile   BusinessProfile `json:"profile"`
	Targeting Targeting       `json:"targeting"`
	Brain     *Brain          `json:"brain,omitempty"`
	Status    AgentStatus     `json:"status"`
	// RequireApproval keeps a human in the loop: no message is sent until approved.
	RequireApproval bool `json:"require_approval"`

	Language         string     `json:"language,omitempty"`    // empty = match the prospect
	BookingURL       string     `json:"booking_url,omitempty"` // e.g. a Calendly link offered to interested prospects
	DefaultDealValue float64    `json:"default_deal_value"`
	Autopilot        bool       `json:"autopilot"`        // scheduler drives the pipeline while the agent is active
	MinScore         int        `json:"min_score"`        // autopilot only contacts prospects scoring at least this
	DailySendLimit   int        `json:"daily_send_limit"` // max outbound messages per rolling 24h
	AutoDiscover     bool       `json:"auto_discover"`
	DiscoverySource  string     `json:"discovery_source,omitempty"`
	LastDiscoveryAt  *time.Time `json:"last_discovery_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
}

type Stage string

const (
	StageNew           Stage = "new"
	StageResearched    Stage = "researched"
	StageContacted     Stage = "contacted"
	StageEngaged       Stage = "engaged"
	StageQualified     Stage = "qualified"
	StageMeetingBooked Stage = "meeting_booked"
	StageWon           Stage = "won"
	StageLost          Stage = "lost"
	StageDoNotContact  Stage = "do_not_contact"
)

type Contact struct {
	Email   string `json:"email,omitempty"`
	Phone   string `json:"phone,omitempty"`
	Website string `json:"website,omitempty"`
}

type Research struct {
	Summary         string    `json:"summary"`
	Needs           []string  `json:"needs"`
	Personalization string    `json:"personalization"`
	ResearchedAt    time.Time `json:"researched_at"`
}

type Prospect struct {
	ID                   string            `json:"id"`
	OrgID                string            `json:"org_id"`
	AgentID              string            `json:"agent_id"`
	Name                 string            `json:"name"`
	ContactName          string            `json:"contact_name,omitempty"`
	Contact              Contact           `json:"contact"`
	Industry             string            `json:"industry,omitempty"`
	Location             string            `json:"location,omitempty"`
	Notes                string            `json:"notes,omitempty"` // facts supplied by the customer or public sources
	Source               string            `json:"source"`
	Stage                Stage             `json:"stage"`
	Score                int               `json:"score"`
	ScoreReasons         []string          `json:"score_reasons,omitempty"`
	Research             *Research         `json:"research,omitempty"`
	Intent               string            `json:"intent,omitempty"`
	MeetingAt            *time.Time        `json:"meeting_at,omitempty"`
	DealValue            float64           `json:"deal_value"`
	WonAt                *time.Time        `json:"won_at,omitempty"`
	CRMID                string            `json:"crm_id,omitempty"` // id in the customer's external CRM once pushed
	QualificationAnswers map[string]string `json:"qualification_answers,omitempty"`
	CreatedAt            time.Time         `json:"created_at"`
	UpdatedAt            time.Time         `json:"updated_at"`
}

type Direction string

const (
	Outbound Direction = "outbound"
	Inbound  Direction = "inbound"
)

type MessageStatus string

const (
	MsgPendingApproval MessageStatus = "pending_approval"
	MsgApproved        MessageStatus = "approved"
	MsgSent            MessageStatus = "sent"
	MsgRejected        MessageStatus = "rejected"
	MsgFailed          MessageStatus = "failed"
	MsgReceived        MessageStatus = "received"
)

type Message struct {
	ID         string        `json:"id"`
	OrgID      string        `json:"org_id"`
	AgentID    string        `json:"agent_id"`
	ProspectID string        `json:"prospect_id"`
	Direction  Direction     `json:"direction"`
	Channel    string        `json:"channel"`
	Kind       string        `json:"kind"` // initial | followup | reply | inbound
	Subject    string        `json:"subject,omitempty"`
	Body       string        `json:"body"`
	Status     MessageStatus `json:"status"`
	Error      string        `json:"error,omitempty"`
	CreatedAt  time.Time     `json:"created_at"`
	SentAt     *time.Time    `json:"sent_at,omitempty"`
}

// Stats powers the dashboard.
type Stats struct {
	ProspectsIdentified int           `json:"prospects_identified"`
	ProspectsResearched int           `json:"prospects_researched"`
	OutreachPrepared    int           `json:"outreach_prepared"`
	OutreachSent        int           `json:"outreach_sent"`
	AwaitingApproval    int           `json:"awaiting_approval"`
	ActiveConversations int           `json:"active_conversations"`
	QualifiedLeads      int           `json:"qualified_leads"`
	MeetingsBooked      int           `json:"meetings_booked"`
	Won                 int           `json:"won"`
	EstimatedPipeline   float64       `json:"estimated_pipeline"` // deal value of qualified + meeting-booked prospects
	WonRevenue          float64       `json:"won_revenue"`
	ByStage             map[Stage]int `json:"by_stage"`
}
