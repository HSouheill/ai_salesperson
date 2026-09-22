// Package queue runs background jobs (research, discovery, autopilot, inbox
// polling) on a bounded worker pool. Jobs are plain data (kind + JSON payload)
// so they can live in Redis: with REDIS_URL set they survive restarts and can
// be shared by several server instances. Without it an in-process backend is
// used (development only: jobs are lost on restart).
package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"
)

var ErrBusy = errors.New("job queue is full")

type Status string

const (
	Queued  Status = "queued"
	Running Status = "running"
	Done    Status = "done"
)

type Job struct {
	ID      string          `json:"id"`
	OrgID   string          `json:"-"`
	Kind    string          `json:"kind"`
	Payload json.RawMessage `json:"-"`
	Status  Status          `json:"status"`
	Total   int             `json:"total"`
	Done    int             `json:"done"`
	Failed  int             `json:"failed"`
	Error   string          `json:"error,omitempty"` // first error seen
	Added   int             `json:"added,omitempty"` // items created (discovery)
}

// Progress is what a handler uses to report work.
type Progress interface {
	// Step records one unit of work finished (err = failed).
	Step(err error)
	SetTotal(n int)
	AddCreated(n int)
}

type Handler func(ctx context.Context, j Job, p Progress) error

// backend stores queue state. Implementations: memory, redis.
type backend interface {
	enqueue(ctx context.Context, j Job, dedupeKey string, ttl time.Duration) (Job, bool, error)
	// next blocks until a job is available (or ctx ends) and marks it running.
	next(ctx context.Context) (Job, error)
	update(ctx context.Context, j Job) error
	load(ctx context.Context, id string) (Job, bool, error)
	ack(ctx context.Context, id string) error
	close()
}

type Queue struct {
	b        backend
	mu       sync.RWMutex
	handlers map[string]Handler
	wg       sync.WaitGroup
}

func newQueue(b backend) *Queue { return &Queue{b: b, handlers: map[string]Handler{}} }

// Register must be called before Start.
func (q *Queue) Register(kind string, h Handler) { q.handlers[kind] = h }

// Enqueue adds a job. With a non-empty dedupeKey, a job with the same key
// enqueued within ttl is not added again (the returned bool reports whether it
// was); the scheduler uses this so instances never double-run an agent.
func (q *Queue) Enqueue(ctx context.Context, j Job, dedupeKey string, ttl time.Duration) (Job, bool, error) {
	if _, ok := q.handlers[j.Kind]; !ok {
		return Job{}, false, fmt.Errorf("unknown job kind %q", j.Kind)
	}
	j.Status = Queued
	return q.b.enqueue(ctx, j, dedupeKey, ttl)
}

// Get returns a job snapshot, only if it belongs to the organization.
func (q *Queue) Get(ctx context.Context, orgID, id string) (Job, bool) {
	j, ok, err := q.b.load(ctx, id)
	if err != nil || !ok || j.OrgID != orgID {
		return Job{}, false
	}
	return j, true
}

// Start launches workers; they stop when ctx ends.
func (q *Queue) Start(ctx context.Context, workers int) {
	for i := 0; i < workers; i++ {
		q.wg.Add(1)
		go func() {
			defer q.wg.Done()
			for ctx.Err() == nil {
				j, err := q.b.next(ctx)
				if err != nil {
					if errors.Is(err, errIdle) {
						continue
					}
					if ctx.Err() == nil {
						log.Printf("queue: %v", err)
						select {
						case <-time.After(time.Second):
						case <-ctx.Done():
						}
					}
					continue
				}
				q.run(ctx, j)
			}
		}()
	}
}

func (q *Queue) run(ctx context.Context, j Job) {
	pr := &progress{q: q, ctx: context.WithoutCancel(ctx), j: &j}
	h := q.handlers[j.Kind]
	func() {
		defer func() {
			if r := recover(); r != nil {
				pr.fail(fmt.Errorf("panic: %v", r))
			}
		}()
		if h == nil {
			pr.fail(fmt.Errorf("no handler for %q", j.Kind))
			return
		}
		if err := h(ctx, j, pr); err != nil {
			pr.fail(err)
		}
	}()
	pr.mu.Lock()
	j.Status = Done
	_ = q.b.update(pr.ctx, j)
	pr.mu.Unlock()
	_ = q.b.ack(pr.ctx, j.ID)
}

// Close waits for workers to finish; cancel the Start context first.
func (q *Queue) Close() { q.wg.Wait(); q.b.close() }

type progress struct {
	q   *Queue
	ctx context.Context
	mu  sync.Mutex
	j   *Job
}

func (p *progress) save() { _ = p.q.b.update(p.ctx, *p.j) }

func (p *progress) Step(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.j.Done++
	if err != nil {
		p.j.Failed++
		if p.j.Error == "" {
			p.j.Error = err.Error()
		}
	}
	p.save()
}
func (p *progress) SetTotal(n int)   { p.mu.Lock(); p.j.Total = n; p.save(); p.mu.Unlock() }
func (p *progress) AddCreated(n int) { p.mu.Lock(); p.j.Added += n; p.save(); p.mu.Unlock() }
func (p *progress) fail(err error) {
	p.mu.Lock()
	p.j.Failed++
	if p.j.Error == "" {
		p.j.Error = err.Error()
	}
	p.mu.Unlock()
}
