package queue

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	keyPending    = "aisp:jobs:pending"
	keyProcessing = "aisp:jobs:processing"
	jobTTL        = 7 * 24 * time.Hour
	maxPending    = 10000
)

type redisBackend struct{ rdb *redis.Client }

// NewRedis returns a durable queue backed by Redis. Jobs that were running
// when a server died are put back on the queue at startup.
func NewRedis(ctx context.Context, url string) (*Queue, error) {
	opt, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("REDIS_URL: %w", err)
	}
	rdb := redis.NewClient(opt)
	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("connect to redis: %w", err)
	}
	// Recover jobs orphaned by a crash.
	for {
		if err := rdb.RPopLPush(ctx, keyProcessing, keyPending).Err(); err != nil {
			break
		}
	}
	return newQueue(&redisBackend{rdb: rdb}), nil
}

func newID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func jobKey(id string) string { return "aisp:job:" + id }

type stored struct {
	Job
	OrgID   string          `json:"org_id"`
	Payload json.RawMessage `json:"payload"`
}

func encode(j Job) ([]byte, error) {
	return json.Marshal(stored{Job: j, OrgID: j.OrgID, Payload: j.Payload})
}
func decode(b []byte) (Job, error) {
	var s stored
	if err := json.Unmarshal(b, &s); err != nil {
		return Job{}, err
	}
	s.Job.OrgID, s.Job.Payload = s.OrgID, s.Payload
	return s.Job, nil
}

func (r *redisBackend) enqueue(ctx context.Context, j Job, key string, ttl time.Duration) (Job, bool, error) {
	if key != "" {
		ok, err := r.rdb.SetNX(ctx, "aisp:dedupe:"+key, 1, ttl).Result()
		if err != nil {
			return Job{}, false, err
		}
		if !ok {
			return Job{}, false, nil
		}
	}
	if n, err := r.rdb.LLen(ctx, keyPending).Result(); err == nil && n >= maxPending {
		return Job{}, false, ErrBusy
	}
	if j.ID == "" {
		j.ID = newID()
	}
	b, err := encode(j)
	if err != nil {
		return Job{}, false, err
	}
	pipe := r.rdb.TxPipeline()
	pipe.Set(ctx, jobKey(j.ID), b, jobTTL)
	pipe.LPush(ctx, keyPending, j.ID)
	if _, err := pipe.Exec(ctx); err != nil {
		return Job{}, false, err
	}
	return j, true, nil
}

func (r *redisBackend) next(ctx context.Context) (Job, error) {
	id, err := r.rdb.BRPopLPush(ctx, keyPending, keyProcessing, 2*time.Second).Result()
	if errors.Is(err, redis.Nil) {
		return Job{}, errIdle
	}
	if err != nil {
		return Job{}, err
	}
	j, ok, err := r.load(ctx, id)
	if err != nil || !ok {
		_ = r.ack(ctx, id)
		return Job{}, fmt.Errorf("job %s missing or unreadable", id)
	}
	j.Status = Running
	return j, r.update(ctx, j)
}

func (r *redisBackend) update(ctx context.Context, j Job) error {
	b, err := encode(j)
	if err != nil {
		return err
	}
	return r.rdb.Set(ctx, jobKey(j.ID), b, jobTTL).Err()
}

func (r *redisBackend) load(ctx context.Context, id string) (Job, bool, error) {
	b, err := r.rdb.Get(ctx, jobKey(id)).Bytes()
	if errors.Is(err, redis.Nil) {
		return Job{}, false, nil
	}
	if err != nil {
		return Job{}, false, err
	}
	j, err := decode(b)
	return j, err == nil, err
}

func (r *redisBackend) ack(ctx context.Context, id string) error {
	return r.rdb.LRem(ctx, keyProcessing, 1, id).Err()
}

func (r *redisBackend) close() { _ = r.rdb.Close() }

// errIdle is returned by next when nothing arrived before the poll timeout.
var errIdle = errors.New("idle")
