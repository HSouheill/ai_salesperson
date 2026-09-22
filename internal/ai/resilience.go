package ai

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"
)

// StatusError is a non-2xx response from a provider.
type StatusError struct {
	Provider string
	Code     int
	Body     string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("%s: status %d: %s", e.Provider, e.Code, e.Body)
}

func retryable(err error) bool {
	var se *StatusError
	if errors.As(err, &se) {
		return se.Code == 429 || se.Code >= 500
	}
	var ne net.Error
	return errors.As(err, &ne) // timeouts and connection failures
}

// Retry retries transient failures (429, 5xx, network) with exponential backoff.
type Retry struct {
	P        Provider
	Attempts int           // default 3
	Backoff  time.Duration // default 1s, doubling
}

func (r Retry) Complete(ctx context.Context, req Request) (string, error) {
	attempts, wait := r.Attempts, r.Backoff
	if attempts == 0 {
		attempts = 3
	}
	if wait == 0 {
		wait = time.Second
	}
	var err error
	for i := 0; i < attempts; i++ {
		var out string
		if out, err = r.P.Complete(ctx, req); err == nil || !retryable(err) || ctx.Err() != nil {
			return out, err
		}
		if i < attempts-1 {
			select {
			case <-time.After(wait):
				wait *= 2
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
	}
	return "", err
}

// Chain tries providers in order and returns the first success.
type Chain []Provider

func (c Chain) Complete(ctx context.Context, req Request) (string, error) {
	var errs []error
	for _, p := range c {
		out, err := p.Complete(ctx, req)
		if err == nil {
			return out, nil
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		errs = append(errs, err)
	}
	return "", errors.Join(errs...)
}
