package domain

// Limits are what a plan allows. The numbers are placeholders to be tuned
// against real costs (AI usage, delivery) and pricing.
type Limits struct {
	MaxAgents    int
	MaxProspects int
	DailySends   int  // per agent, rolling 24h
	AutoSend     bool // send without human approval
	Integrations bool // CRM webhook / HubSpot push
	APIAccess    bool // API keys
	WhiteLabel   bool // agency: client organizations and branding
}

func (p Plan) Limits() Limits {
	switch p {
	case PlanGrowth:
		return Limits{MaxAgents: 3, MaxProspects: 5000, DailySends: 100}
	case PlanPro:
		return Limits{MaxAgents: 10, MaxProspects: 50000, DailySends: 500, AutoSend: true, Integrations: true, APIAccess: true}
	case PlanEnterprise:
		return Limits{MaxAgents: 100, MaxProspects: 500000, DailySends: 2000, AutoSend: true, Integrations: true, APIAccess: true, WhiteLabel: true}
	case PlanStarter:
		return Limits{MaxAgents: 1, MaxProspects: 500, DailySends: 25}
	default: // trial
		return Limits{MaxAgents: 1, MaxProspects: 200, DailySends: 20}
	}
}

// PlanInfo is what the pricing page shows.
type PlanInfo struct {
	Plan       Plan     `json:"plan"`
	Name       string   `json:"name"`
	PriceUSD   int      `json:"price_usd_month"` // 0 = contact sales
	Highlights []string `json:"highlights"`
	Limits     Limits   `json:"limits"`
}

func PublicPlans() []PlanInfo {
	mk := func(p Plan, name string, price int, hl ...string) PlanInfo {
		return PlanInfo{Plan: p, Name: name, PriceUSD: price, Highlights: hl, Limits: p.Limits()}
	}
	return []PlanInfo{
		mk(PlanStarter, "Starter", 49, "One sales agent", "Basic prospecting", "Human approval on every message"),
		mk(PlanGrowth, "Growth", 149, "Multiple campaigns", "Lead intelligence & AI conversations", "Built-in CRM & analytics"),
		mk(PlanPro, "Pro", 399, "Multiple agents", "Auto-send automation", "CRM integrations & API access", "Higher usage limits"),
		mk(PlanEnterprise, "Enterprise", 0, "White-label for agencies", "Client organizations with their own branding", "Dedicated or self-hosted deployment"),
	}
}
