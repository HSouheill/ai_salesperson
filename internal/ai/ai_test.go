package ai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type stub struct {
	calls atomic.Int32
	fn    func(n int) (string, error)
}

func (s *stub) Complete(context.Context, Request) (string, error) { return s.fn(int(s.calls.Add(1))) }

func TestRetryRetriesTransientErrorsOnly(t *testing.T) {
	flaky := &stub{fn: func(n int) (string, error) {
		if n < 3 {
			return "", &StatusError{Provider: "x", Code: 503}
		}
		return "ok", nil
	}}
	out, err := Retry{P: flaky, Backoff: time.Millisecond}.Complete(context.Background(), Request{})
	if err != nil || out != "ok" || flaky.calls.Load() != 3 {
		t.Fatalf("out=%q err=%v calls=%d", out, err, flaky.calls.Load())
	}
	rate := &stub{fn: func(n int) (string, error) { return "", &StatusError{Provider: "x", Code: 429} }}
	if _, err := (Retry{P: rate, Backoff: time.Millisecond}).Complete(context.Background(), Request{}); err == nil || rate.calls.Load() != 3 {
		t.Fatalf("429 should be retried then fail: %v calls=%d", err, rate.calls.Load())
	}
	bad := &stub{fn: func(int) (string, error) { return "", &StatusError{Provider: "x", Code: 400} }}
	if _, err := (Retry{P: bad, Backoff: time.Millisecond}).Complete(context.Background(), Request{}); err == nil || bad.calls.Load() != 1 {
		t.Fatalf("400 must not be retried: calls=%d", bad.calls.Load())
	}
}

func TestChainFallsBack(t *testing.T) {
	down := &stub{fn: func(int) (string, error) { return "", errors.New("down") }}
	up := &stub{fn: func(int) (string, error) { return "from-second", nil }}
	if out, err := (Chain{down, up}).Complete(context.Background(), Request{}); err != nil || out != "from-second" {
		t.Fatalf("%q %v", out, err)
	}
	if _, err := (Chain{down, down}).Complete(context.Background(), Request{}); err == nil {
		t.Fatal("all providers down must be an error")
	}
}

func TestOpenAICompatible(t *testing.T) {
	var got map[string]any
	var auth, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &got)
		auth, path = r.Header.Get("Authorization"), r.URL.Path
		w.Write([]byte(`{"choices":[{"message":{"content":"Sure. {\"a\": 5}"}}]}`))
	}))
	defer srv.Close()

	o := NewOpenAI("k", "some-model", srv.URL+"/v1")
	var out struct{ A int }
	if err := CompleteJSON(context.Background(), o, Request{System: "sys", Prompt: "p", MaxTokens: 99}, &out); err != nil || out.A != 5 {
		t.Fatalf("%v %+v", err, out)
	}
	msgs := got["messages"].([]any)
	if path != "/v1/chat/completions" || auth != "Bearer k" || got["model"] != "some-model" || got["max_tokens"] != float64(99) ||
		!strings.Contains(msgs[0].(map[string]any)["content"].(string), "single JSON object") {
		t.Errorf("request wrong: path=%s auth=%s body=%v", path, auth, got)
	}
	// Servers without a key (Ollama, vLLM) must not get an empty bearer header.
	auth = "unset"
	NewOpenAI("", "m", srv.URL).Complete(context.Background(), Request{})
	if auth != "" {
		t.Errorf("no key => no Authorization header, got %q", auth)
	}
}

func TestCompleteJSONReportsUpstreamErrors(t *testing.T) {
	junk := &stub{fn: func(int) (string, error) { return "no json here", nil }}
	if err := CompleteJSON(context.Background(), junk, Request{}, &struct{}{}); !errors.Is(err, ErrUpstream) {
		t.Fatalf("unusable output should be ErrUpstream, got %v", err)
	}
}
