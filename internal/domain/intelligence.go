package domain

import "time"

// Company Intelligence: given a company name (any company — the customer's
// own business, a prospect, or an explicit competitor), the AI searches the
// live web, collects what's publicly known about it, identifies real named
// competitors and what they do better, and turns that into prioritized,
// concrete recommendations for closing the gap.

type ReportStatus string

const (
	ReportRunning ReportStatus = "running"
	ReportDone    ReportStatus = "done"
	ReportFailed  ReportStatus = "failed"
)

type CompanyProfile struct {
	Summary      string   `json:"summary"`
	Industry     string   `json:"industry"`
	Products     []string `json:"products,omitempty"`
	Services     []string `json:"services,omitempty"`
	TargetMarket string   `json:"target_market,omitempty"`
	Locations    []string `json:"locations,omitempty"`
	FoundedYear  string   `json:"founded_year,omitempty"`
	CompanySize  string   `json:"company_size,omitempty"`
	ValueProp    string   `json:"value_proposition,omitempty"`
	Strengths    []string `json:"strengths,omitempty"`
	// Signals are concrete, time-bound facts found during research (a launch,
	// funding round, expansion, hiring push, …), not generic praise.
	Signals []string `json:"signals,omitempty"`
}

type Competitor struct {
	Name       string   `json:"name"`
	Website    string   `json:"website,omitempty"`
	Summary    string   `json:"summary"`
	Advantages []string `json:"advantages"` // concrete things this competitor does better
}

type Severity string

const (
	SeverityHigh   Severity = "high"
	SeverityMedium Severity = "medium"
	SeverityLow    Severity = "low"
)

// Gap is something competitors have that the researched company doesn't,
// grounded in what research actually found (not a guess).
type Gap struct {
	Area        string   `json:"area"`
	Description string   `json:"description"`
	Severity    Severity `json:"severity"`
	Evidence    string   `json:"evidence"`
}

type Effort string

const (
	EffortLow    Effort = "low"
	EffortMedium Effort = "medium"
	EffortHigh   Effort = "high"
)

type Recommendation struct {
	GapArea  string `json:"gap_area"` // matches a Gap.Area
	Action   string `json:"action"`
	Impact   string `json:"impact"`
	Effort   Effort `json:"effort"`
	Priority int    `json:"priority"` // 1 = highest
}

type ReportSource struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

type IntelligenceReport struct {
	ID          string       `json:"id"`
	OrgID       string       `json:"org_id"`
	CompanyName string       `json:"company_name"`
	Website     string       `json:"website,omitempty"`
	Status      ReportStatus `json:"status"`
	Error       string       `json:"error,omitempty"`
	CreatedAt   time.Time    `json:"created_at"`
	CompletedAt *time.Time   `json:"completed_at,omitempty"`

	Profile         CompanyProfile   `json:"profile"`
	Competitors     []Competitor     `json:"competitors,omitempty"`
	Gaps            []Gap            `json:"gaps,omitempty"`
	Recommendations []Recommendation `json:"recommendations,omitempty"`
	Sources         []ReportSource   `json:"sources,omitempty"`
}
