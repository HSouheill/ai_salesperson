// Package errtrack reports unexpected errors and panics to Sentry when
// SENTRY_DSN is configured. Every function is a safe no-op when it is not —
// the server still logs to stdout either way (see internal/httpapi logRequests
// and fail), this only adds an aggregated, alertable view on top.
package errtrack

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/getsentry/sentry-go"
)

var enabled bool

// Init configures the SDK. env/release are attached to every event so errors
// can be filtered by deployment. Call Flush before the process exits.
func Init(dsn, env, release string) {
	if dsn == "" {
		return
	}
	err := sentry.Init(sentry.ClientOptions{
		Dsn: dsn, Environment: env, Release: release,
		// Errors here are infrastructure problems, not user traffic: send every one.
		SampleRate: 1.0,
	})
	if err != nil {
		log.Printf("errtrack: Sentry did not start: %v", err)
		return
	}
	enabled = true
	log.Println("errtrack: Sentry error reporting enabled")
}

// Flush waits briefly for queued events to send; call during shutdown.
func Flush() {
	if enabled {
		sentry.Flush(2 * time.Second)
	}
}

// Capture reports an unexpected error.
func Capture(err error) {
	if !enabled || err == nil {
		return
	}
	sentry.CaptureException(err)
}

// Recover reports a panic recovered by middleware and re-returns it so the
// caller's existing recovery/500 logic still runs unchanged.
func Recover(r *http.Request, rec any) {
	if !enabled || rec == nil {
		return
	}
	hub := sentry.CurrentHub().Clone()
	if r != nil {
		hub.Scope().SetRequest(r)
	}
	hub.RecoverWithContext(context.Background(), rec)
}
