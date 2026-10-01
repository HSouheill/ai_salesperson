package ratelimit

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

func suite(t *testing.T, mk func() Limiter) {
	t.Run("allows up to max then blocks", func(t *testing.T) {
		l := mk()
		defer l.Close()
		ctx := context.Background()
		for i := 0; i < 3; i++ {
			if !l.Allow(ctx, "k1", 3, time.Minute) {
				t.Fatalf("call %d should be allowed", i)
			}
		}
		if l.Allow(ctx, "k1", 3, time.Minute) {
			t.Fatal("4th call should be blocked")
		}
	})

	t.Run("keys are independent", func(t *testing.T) {
		l := mk()
		defer l.Close()
		ctx := context.Background()
		for i := 0; i < 3; i++ {
			l.Allow(ctx, "a", 3, time.Minute)
		}
		if !l.Allow(ctx, "b", 3, time.Minute) {
			t.Fatal("a different key must have its own budget")
		}
		if l.Allow(ctx, "a", 3, time.Minute) {
			t.Fatal("key a should still be exhausted")
		}
	})

	t.Run("resets after the window", func(t *testing.T) {
		l := mk()
		defer l.Close()
		ctx := context.Background()
		for i := 0; i < 2; i++ {
			l.Allow(ctx, "w", 2, 150*time.Millisecond)
		}
		if l.Allow(ctx, "w", 2, 150*time.Millisecond) {
			t.Fatal("should be exhausted")
		}
		time.Sleep(350 * time.Millisecond)
		if !l.Allow(ctx, "w", 2, 150*time.Millisecond) {
			t.Fatal("should have reset after the window elapsed")
		}
	})
}

func TestMemoryLimiter(t *testing.T) { suite(t, func() Limiter { return NewMemory() }) }

func TestRedisLimiter(t *testing.T) {
	suite(t, func() Limiter {
		mr := miniredis.RunT(t)
		l, err := New("redis://" + mr.Addr())
		if err != nil {
			t.Fatal(err)
		}
		return l
	})
}

// Two independent limiter instances sharing the same Redis enforce one shared
// budget — this is the whole point of moving off the in-process limiter.
func TestRedisLimiterIsSharedAcrossInstances(t *testing.T) {
	mr := miniredis.RunT(t)
	ctx := context.Background()
	l1, err := New("redis://" + mr.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer l1.Close()
	l2, err := New("redis://" + mr.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer l2.Close()
	for i := 0; i < 5; i++ {
		l1.Allow(ctx, "shared", 5, time.Minute)
	}
	if l2.Allow(ctx, "shared", 5, time.Minute) {
		t.Fatal("a second instance must see the first instance's usage")
	}
}

func TestNewPicksBackend(t *testing.T) {
	l, err := New("")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := l.(*memLimiter); !ok {
		t.Errorf("empty URL should give a memory limiter, got %T", l)
	}
	if _, err := New("not-a-url://###"); err == nil {
		t.Error("a malformed REDIS_URL should fail fast, not silently fall back")
	}
}
