package sales

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hussein/ai-salesperson/internal/ai"
	"github.com/hussein/ai-salesperson/internal/domain"
	"github.com/hussein/ai-salesperson/internal/store"
)

const (
	researchBatch     = 10
	outreachBatch     = 10
	discoveryEvery    = 6 * time.Hour
	discoveryBatch    = 40
	discoverBelowPool = 30 // discover more when fewer than this many uncontacted prospects remain
)

type AutopilotReport struct {
	Discovered int      `json:"discovered"`
	Researched int      `json:"researched"`
	Drafted    int      `json:"drafted"`
	Sent       int      `json:"sent"`
	FollowUps  int      `json:"follow_ups"`
	Notes      []string `json:"notes,omitempty"`
}

// Autopilot advances one agent's pipeline a bounded step: discover → research →
// personalize outreach → follow up. It respects the agent's approval setting,
// minimum score and daily send limit, so a tick never does unbounded work.
func (s *Service) Autopilot(ctx context.Context, orgID, agentID string) (AutopilotReport, error) {
	var rep AutopilotReport
	org, err := s.entitledOrg(ctx, orgID)
	if err != nil {
		return rep, err
	}
	a, err := s.Store.GetAgent(ctx, orgID, agentID)
	if err != nil {
		return rep, err
	}
	if a.Status != domain.AgentActive || a.Brain == nil {
		return rep, fmt.Errorf("%w: agent is not active", ErrPrecondition)
	}
	note := func(f string, args ...any) { rep.Notes = append(rep.Notes, fmt.Sprintf(f, args...)) }
	stop := func(err error) bool { // errors that make further work pointless this tick
		return errors.Is(err, ai.ErrUpstream) || errors.Is(err, ErrLimit) || errors.Is(err, context.Canceled)
	}

	// 1. Discovery keeps the pipeline stocked.
	if a.AutoDiscover && (a.LastDiscoveryAt == nil || s.Now().Sub(*a.LastDiscoveryAt) >= discoveryEvery) {
		pool, err := s.Store.ListProspects(ctx, orgID, store.ProspectFilter{AgentID: agentID})
		if err != nil {
			return rep, err
		}
		fresh := 0
		for _, p := range pool {
			if p.Stage == domain.StageNew || p.Stage == domain.StageResearched {
				fresh++
			}
		}
		if pairs := discoveryPairs(a); fresh < discoverBelowPool && len(pairs) > 0 {
			pair := pairs[int(s.Now().Unix()/int64(discoveryEvery/time.Second))%len(pairs)]
			res, err := s.Discover(ctx, orgID, agentID, DiscoverInput{Industry: pair[0], Location: pair[1], Limit: discoveryBatch})
			now := s.Now()
			a.LastDiscoveryAt = &now // also on failure, so a broken source is not hammered every tick
			_ = s.Store.PutAgent(ctx, a)
			if err != nil {
				note("discovery failed: %v", err)
			} else {
				rep.Discovered = res.Added
			}
		}
	}

	// 2. Research a batch of new prospects.
	news, err := s.Store.ListProspects(ctx, orgID, store.ProspectFilter{AgentID: agentID, Stage: domain.StageNew})
	if err != nil {
		return rep, err
	}
	for i, p := range news {
		if i >= researchBatch {
			break
		}
		if _, err := s.ResearchProspect(ctx, orgID, p.ID); err != nil {
			if stop(err) {
				return rep, err
			}
			note("research %s: %v", p.Name, err)
			continue
		}
		rep.Researched++
	}

	// 3. Personalized outreach to the best-scoring researched prospects.
	room, err := s.outreachRoom(ctx, org, a)
	if err != nil {
		return rep, err
	}
	researched, err := s.Store.ListProspects(ctx, orgID, store.ProspectFilter{AgentID: agentID, Stage: domain.StageResearched})
	if err != nil {
		return rep, err
	}
	ch, chErr := s.Channels.For(org, a.Channel)
	drafts := 0
	for _, p := range sortByScore(researched) {
		if room <= 0 || drafts >= outreachBatch || chErr != nil {
			break
		}
		if p.Score < a.MinScore || ch.Recipient(p.Contact.Email, p.Contact.Phone) == "" {
			continue
		}
		if ms, err := s.Store.ListMessages(ctx, orgID, store.MessageFilter{ProspectID: p.ID}); err != nil || len(ms) > 0 {
			continue // already drafted (possibly rejected by a human): do not redraft
		}
		m, err := s.draft(ctx, org, a, p, "initial", "Write the first outreach message.")
		if err != nil {
			if stop(err) {
				return rep, err
			}
			note("outreach %s: %v", p.Name, err)
			continue
		}
		drafts++
		room--
		rep.Drafted++
		if m.Status == domain.MsgSent {
			rep.Sent++
		}
	}
	if chErr != nil {
		note("outreach skipped: %v", chErr)
	}

	// 4. Follow-ups that are due.
	if room > 0 {
		fu, err := s.RunFollowUps(ctx, orgID, agentID, room)
		if err != nil {
			if stop(err) {
				return rep, err
			}
			note("follow-ups: %v", err)
		}
		rep.FollowUps = len(fu)
	}
	return rep, nil
}

// outreachRoom is how many more messages this agent may prepare now: the
// daily cap minus what was sent in the last 24h and what is still waiting.
func (s *Service) outreachRoom(ctx context.Context, org domain.Org, a domain.Agent) (int, error) {
	sent, err := s.Store.CountSent(ctx, org.ID, a.ID, s.Now().Add(-24*time.Hour))
	if err != nil {
		return 0, err
	}
	pending, err := s.Store.ListMessages(ctx, org.ID, store.MessageFilter{Status: domain.MsgPendingApproval})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, m := range pending {
		if m.AgentID == a.ID && m.Direction == domain.Outbound {
			n++
		}
	}
	return sendLimit(org, a) - sent - n, nil
}

func discoveryPairs(a domain.Agent) [][2]string {
	var out [][2]string
	for _, i := range a.Targeting.Industries {
		for _, l := range a.Targeting.Locations {
			out = append(out, [2]string{i, l})
		}
	}
	return out
}

func sortByScore(ps []domain.Prospect) []domain.Prospect {
	out := append([]domain.Prospect(nil), ps...)
	for i := 1; i < len(out); i++ { // insertion sort: batches are small
		for j := i; j > 0 && out[j].Score > out[j-1].Score; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
