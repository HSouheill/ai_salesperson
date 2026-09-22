// Package scheduler makes the agents work unattended: on every tick it queues
// one autopilot step per active agent and one inbox poll per connected mailbox.
package scheduler

import (
	"context"
	"log"
	"time"

	"github.com/hussein/ai-salesperson/internal/jobs"
	"github.com/hussein/ai-salesperson/internal/store"
)

type Scheduler struct {
	Store    store.Store
	Jobs     *jobs.Runner
	Interval time.Duration
}

// Run blocks until ctx ends. Jobs are deduplicated in the queue, so several
// server instances can run a scheduler without doubling the work.
func (s *Scheduler) Run(ctx context.Context) {
	t := time.NewTicker(s.Interval)
	defer t.Stop()
	s.Tick(ctx)
	for {
		select {
		case <-t.C:
			s.Tick(ctx)
		case <-ctx.Done():
			return
		}
	}
}

func (s *Scheduler) Tick(ctx context.Context) {
	ttl := max(s.Interval-5*time.Second, 10*time.Second)
	agents, err := s.Store.ListActiveAgents(ctx)
	if err != nil {
		log.Printf("scheduler: list agents: %v", err)
	}
	queued := 0
	for _, a := range agents {
		if !a.Autopilot {
			continue
		}
		if _, ok, err := s.Jobs.Autopilot(ctx, a.OrgID, a.ID, ttl); err != nil {
			log.Printf("scheduler: queue autopilot %s: %v", a.ID, err)
		} else if ok {
			queued++
		}
	}
	orgs, err := s.Store.ListInboxOrgs(ctx)
	if err != nil {
		log.Printf("scheduler: list inboxes: %v", err)
	}
	for _, o := range orgs {
		if _, ok, err := s.Jobs.PollInbox(ctx, o.ID, ttl); err != nil {
			log.Printf("scheduler: queue inbox poll %s: %v", o.ID, err)
		} else if ok {
			queued++
		}
	}
	if queued > 0 {
		log.Printf("scheduler: queued %d job(s)", queued)
	}
}
