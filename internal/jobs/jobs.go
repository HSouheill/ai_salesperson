// Package jobs connects the queue to the sales pipeline: it defines the
// background job kinds and how to run them.
package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hussein/ai-salesperson/internal/domain"
	"github.com/hussein/ai-salesperson/internal/inbound"
	"github.com/hussein/ai-salesperson/internal/queue"
	"github.com/hussein/ai-salesperson/internal/sales"
	"github.com/hussein/ai-salesperson/internal/store"
)

const (
	KindResearch  = "research"
	KindDiscover  = "discover"
	KindAutopilot = "autopilot"
	KindPollInbox = "poll_inbox"

	maxResearchPerJob = 500
)

type Runner struct {
	Q      *queue.Queue
	Sales  *sales.Service
	Store  store.Store
	Poller *inbound.Poller
}

type agentPayload struct {
	AgentID string `json:"agent_id"`
}

type discoverPayload struct {
	AgentID string              `json:"agent_id"`
	Input   sales.DiscoverInput `json:"input"`
}

// Register attaches the handlers; call before the queue starts.
func (r *Runner) Register() {
	r.Q.Register(KindResearch, func(ctx context.Context, j queue.Job, p queue.Progress) error {
		var pl agentPayload
		if err := json.Unmarshal(j.Payload, &pl); err != nil {
			return err
		}
		ps, err := r.Store.ListProspects(ctx, j.OrgID, store.ProspectFilter{AgentID: pl.AgentID, Stage: domain.StageNew})
		if err != nil {
			return err
		}
		if len(ps) > maxResearchPerJob {
			ps = ps[:maxResearchPerJob]
		}
		p.SetTotal(len(ps))
		for _, x := range ps {
			_, err := r.Sales.ResearchProspect(ctx, j.OrgID, x.ID)
			p.Step(err)
		}
		return nil
	})
	r.Q.Register(KindDiscover, func(ctx context.Context, j queue.Job, p queue.Progress) error {
		var pl discoverPayload
		if err := json.Unmarshal(j.Payload, &pl); err != nil {
			return err
		}
		p.SetTotal(1)
		res, err := r.Sales.Discover(ctx, j.OrgID, pl.AgentID, pl.Input)
		p.AddCreated(res.Added)
		p.Step(err)
		return nil
	})
	r.Q.Register(KindAutopilot, func(ctx context.Context, j queue.Job, p queue.Progress) error {
		var pl agentPayload
		if err := json.Unmarshal(j.Payload, &pl); err != nil {
			return err
		}
		p.SetTotal(1)
		rep, err := r.Sales.Autopilot(ctx, j.OrgID, pl.AgentID)
		p.AddCreated(rep.Discovered + rep.Drafted)
		p.Step(err)
		return nil
	})
	r.Q.Register(KindPollInbox, func(ctx context.Context, j queue.Job, p queue.Progress) error {
		org, err := r.Store.GetOrg(ctx, j.OrgID)
		if err != nil {
			return err
		}
		cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		p.SetTotal(1)
		res, err := r.Poller.PollOrg(cctx, org)
		p.AddCreated(res.Matched)
		p.Step(err)
		return nil
	})
}

func (r *Runner) enqueue(ctx context.Context, orgID, kind string, payload any, dedupe string, ttl time.Duration) (queue.Job, bool, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return queue.Job{}, false, err
	}
	return r.Q.Enqueue(ctx, queue.Job{OrgID: orgID, Kind: kind, Payload: b}, dedupe, ttl)
}

func (r *Runner) Research(ctx context.Context, orgID, agentID string) (queue.Job, error) {
	j, _, err := r.enqueue(ctx, orgID, KindResearch, agentPayload{agentID}, "", 0)
	return j, err
}

func (r *Runner) Discover(ctx context.Context, orgID, agentID string, in sales.DiscoverInput) (queue.Job, error) {
	j, _, err := r.enqueue(ctx, orgID, KindDiscover, discoverPayload{agentID, in}, "", 0)
	return j, err
}

// Autopilot queues one autopilot step. With ttl > 0 the same agent is not
// queued twice within ttl (used by the scheduler); ok reports whether it was queued.
func (r *Runner) Autopilot(ctx context.Context, orgID, agentID string, ttl time.Duration) (queue.Job, bool, error) {
	key := ""
	if ttl > 0 {
		key = fmt.Sprintf("autopilot:%s", agentID)
	}
	return r.enqueue(ctx, orgID, KindAutopilot, agentPayload{agentID}, key, ttl)
}

func (r *Runner) PollInbox(ctx context.Context, orgID string, ttl time.Duration) (queue.Job, bool, error) {
	key := ""
	if ttl > 0 {
		key = "poll:" + orgID
	}
	return r.enqueue(ctx, orgID, KindPollInbox, struct{}{}, key, ttl)
}
