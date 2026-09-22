package queue

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

func waitDone(t *testing.T, q *Queue, org, id string) Job {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if j, ok := q.Get(context.Background(), org, id); ok && j.Status == Done {
			return j
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("job did not finish")
	return Job{}
}

// suite runs the same behaviour checks against every backend.
func suite(t *testing.T, mk func(t *testing.T) *Queue) {
	t.Run("runs jobs with progress and scopes them by org", func(t *testing.T) {
		q := mk(t)
		q.Register("work", func(_ context.Context, j Job, p Progress) error {
			var n struct{ N int }
			_ = json.Unmarshal(j.Payload, &n)
			p.SetTotal(n.N)
			for i := 0; i < n.N; i++ {
				if i == 1 {
					p.Step(errors.New("boom"))
				} else {
					p.Step(nil)
				}
			}
			p.AddCreated(7)
			return nil
		})
		ctx, cancel := context.WithCancel(context.Background())
		defer func() { cancel(); q.Close() }()
		q.Start(ctx, 2)

		j, ok, err := q.Enqueue(ctx, Job{OrgID: "o1", Kind: "work", Payload: json.RawMessage(`{"N":3}`)}, "", 0)
		if err != nil || !ok {
			t.Fatal(err, ok)
		}
		got := waitDone(t, q, "o1", j.ID)
		if got.Total != 3 || got.Done != 3 || got.Failed != 1 || got.Error != "boom" || got.Added != 7 {
			t.Fatalf("job = %+v", got)
		}
		if _, ok := q.Get(ctx, "other-org", j.ID); ok {
			t.Fatal("a job must not be visible to another organization")
		}
	})

	t.Run("dedupes by key and rejects unknown kinds", func(t *testing.T) {
		q := mk(t)
		var runs atomic.Int32
		q.Register("tick", func(context.Context, Job, Progress) error { runs.Add(1); return nil })
		ctx, cancel := context.WithCancel(context.Background())
		defer func() { cancel(); q.Close() }()
		q.Start(ctx, 1)

		_, first, _ := q.Enqueue(ctx, Job{OrgID: "o", Kind: "tick"}, "agent:1", time.Minute)
		_, second, _ := q.Enqueue(ctx, Job{OrgID: "o", Kind: "tick"}, "agent:1", time.Minute)
		if !first || second {
			t.Fatalf("first=%v second=%v; want true,false", first, second)
		}
		if _, _, err := q.Enqueue(ctx, Job{OrgID: "o", Kind: "nope"}, "", 0); err == nil {
			t.Fatal("unknown kind must be rejected")
		}
		time.Sleep(200 * time.Millisecond)
		if runs.Load() != 1 {
			t.Fatalf("handler ran %d times", runs.Load())
		}
	})

	t.Run("a panicking handler fails the job, not the worker", func(t *testing.T) {
		q := mk(t)
		q.Register("bad", func(context.Context, Job, Progress) error { panic("kaboom") })
		q.Register("ok", func(_ context.Context, _ Job, p Progress) error { p.Step(nil); return nil })
		ctx, cancel := context.WithCancel(context.Background())
		defer func() { cancel(); q.Close() }()
		q.Start(ctx, 1)
		bad, _, _ := q.Enqueue(ctx, Job{OrgID: "o", Kind: "bad"}, "", 0)
		good, _, _ := q.Enqueue(ctx, Job{OrgID: "o", Kind: "ok"}, "", 0)
		if j := waitDone(t, q, "o", bad.ID); j.Failed != 1 || j.Error == "" {
			t.Fatalf("bad job = %+v", j)
		}
		if j := waitDone(t, q, "o", good.ID); j.Done != 1 {
			t.Fatalf("worker died after a panic: %+v", j)
		}
	})
}

func TestMemoryQueue(t *testing.T) { suite(t, func(*testing.T) *Queue { return NewMemory(16) }) }

func TestRedisQueue(t *testing.T) {
	suite(t, func(t *testing.T) *Queue {
		mr := miniredis.RunT(t)
		q, err := NewRedis(context.Background(), "redis://"+mr.Addr())
		if err != nil {
			t.Fatal(err)
		}
		return q
	})
}

// A job is not lost when the server that took it dies, and another instance can pick it up.
func TestRedisRecoversJobsFromACrashedWorker(t *testing.T) {
	mr := miniredis.RunT(t)
	url := "redis://" + mr.Addr()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q1, err := NewRedis(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	q1.Register("work", func(context.Context, Job, Progress) error { return nil })
	j, _, err := q1.Enqueue(ctx, Job{OrgID: "o", Kind: "work"}, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a worker that took the job and then crashed before finishing.
	id, _ := mr.Lpop("aisp:jobs:pending")
	mr.Push("aisp:jobs:processing", id)

	q2, err := NewRedis(ctx, url) // the restarted server recovers orphaned jobs
	if err != nil {
		t.Fatal(err)
	}
	var ran atomic.Int32
	q2.Register("work", func(context.Context, Job, Progress) error { ran.Add(1); return nil })
	q2.Start(ctx, 1)
	waitDone(t, q2, "o", j.ID)
	if ran.Load() != 1 {
		t.Fatalf("recovered job ran %d times", ran.Load())
	}
	if n, _ := mr.List("aisp:jobs:processing"); len(n) != 0 {
		t.Fatalf("processing list not cleaned: %v", n)
	}
}
