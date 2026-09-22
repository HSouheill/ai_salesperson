package queue

import (
	"context"
	"sync"
	"time"
)

type memBackend struct {
	mu     sync.Mutex
	jobs   map[string]Job
	q      chan string
	dedupe map[string]time.Time
}

// NewMemory returns an in-process queue (not durable).
func NewMemory(size int) *Queue {
	return newQueue(&memBackend{jobs: map[string]Job{}, q: make(chan string, size), dedupe: map[string]time.Time{}})
}

func (m *memBackend) enqueue(_ context.Context, j Job, key string, ttl time.Duration) (Job, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if key != "" {
		if until, ok := m.dedupe[key]; ok && time.Now().Before(until) {
			return Job{}, false, nil
		}
	}
	if j.ID == "" {
		j.ID = newID()
	}
	select {
	case m.q <- j.ID:
	default:
		return Job{}, false, ErrBusy
	}
	if key != "" {
		m.dedupe[key] = time.Now().Add(ttl)
	}
	m.jobs[j.ID] = j
	return j, true, nil
}

func (m *memBackend) next(ctx context.Context) (Job, error) {
	select {
	case id := <-m.q:
		m.mu.Lock()
		defer m.mu.Unlock()
		j := m.jobs[id]
		j.Status = Running
		m.jobs[id] = j
		return j, nil
	case <-ctx.Done():
		return Job{}, ctx.Err()
	}
}

func (m *memBackend) update(_ context.Context, j Job) error {
	m.mu.Lock()
	m.jobs[j.ID] = j
	m.mu.Unlock()
	return nil
}

func (m *memBackend) load(_ context.Context, id string) (Job, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	return j, ok, nil
}

func (m *memBackend) ack(context.Context, string) error { return nil }
func (m *memBackend) close()                            {}
