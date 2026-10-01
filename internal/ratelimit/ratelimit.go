// Package ratelimit throttles actions by key (IP, org ID, …). With REDIS_URL
// set it is backed by Redis, so the limit holds across every server instance;
// without it, it falls back to an in-process limiter (correct for one
// instance only — a second instance would get its own separate budget).
package ratelimit

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// Limiter enforces "at most max events per window" per key.
type Limiter interface {
	// Allow records one event for key and reports whether it is within budget.
	Allow(ctx context.Context, key string, max int, window time.Duration) bool
	Close()
}

// New returns a Redis-backed limiter when redisURL is set, else an in-process one.
func New(redisURL string) (Limiter, error) {
	if redisURL == "" {
		return NewMemory(), nil
	}
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("REDIS_URL: %w", err)
	}
	return &redisLimiter{rdb: redis.NewClient(opt)}, nil
}

// ---- Redis backend: fixed window via INCR+EXPIRE (atomic, no Lua needed) ----

type redisLimiter struct{ rdb *redis.Client }

func (r *redisLimiter) Allow(ctx context.Context, key string, max int, window time.Duration) bool {
	bucket := time.Now().UnixNano() / int64(window) // one counter per window per key
	rk := fmt.Sprintf("aisp:rl:%s:%d", key, bucket)
	pipe := r.rdb.TxPipeline()
	incr := pipe.Incr(ctx, rk)
	pipe.Expire(ctx, rk, window+time.Second)
	if _, err := pipe.Exec(ctx); err != nil {
		return true // Redis unreachable: fail open rather than lock everyone out
	}
	return incr.Val() <= int64(max)
}

func (r *redisLimiter) Close() { _ = r.rdb.Close() }

// ---- In-process backend: sliding window over a timestamp slice -------------

type memLimiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

func NewMemory() Limiter { return &memLimiter{hits: map[string][]time.Time{}} }

func (m *memLimiter) Allow(_ context.Context, key string, max int, window time.Duration) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	recent := m.hits[key][:0]
	for _, t := range m.hits[key] {
		if now.Sub(t) < window {
			recent = append(recent, t)
		}
	}
	if len(recent) >= max {
		m.hits[key] = recent
		return false
	}
	m.hits[key] = append(recent, now)
	if len(m.hits) > 10000 { // bound memory
		for k, v := range m.hits {
			if len(v) == 0 || now.Sub(v[len(v)-1]) > window {
				delete(m.hits, k)
			}
		}
	}
	return true
}

func (m *memLimiter) Close() {}
