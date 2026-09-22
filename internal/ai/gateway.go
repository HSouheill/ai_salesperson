// Package ai is the AI gateway: the platform talks to models only through
// Provider, so no part of the system is locked to a single vendor.
package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/hussein/ai-salesperson/internal/config"
)

// Task identifies what a request is for. Real providers ignore it; the mock
// uses it to return canned output.
type Task string

const (
	TaskProfile  Task = "profile"
	TaskBrain    Task = "brain"
	TaskResearch Task = "research"
	TaskOutreach Task = "outreach"
	TaskReply    Task = "reply"
)

type Request struct {
	Task      Task
	System    string
	Prompt    string
	MaxTokens int
}

// ErrUpstream marks failures of the model provider (network, status, bad output).
var ErrUpstream = errors.New("ai provider error")

type Provider interface {
	Complete(ctx context.Context, r Request) (string, error)
}

// New builds the configured provider, optionally chained to a fallback, with
// retries on transient failures.
func New(c config.Config) (Provider, error) {
	primary, err := build(c, c.AIProvider, false)
	if err != nil {
		return nil, err
	}
	if c.AIFallback == "" || c.AIFallback == c.AIProvider {
		return Retry{P: primary}, nil
	}
	second, err := build(c, c.AIFallback, true)
	if err != nil {
		return nil, fmt.Errorf("fallback: %w", err)
	}
	return Chain{Retry{P: primary}, Retry{P: second}}, nil
}

func build(c config.Config, name string, fallback bool) (Provider, error) {
	model := c.AIModel
	if fallback && c.FallbackModel != "" {
		model = c.FallbackModel
	}
	switch name {
	case "anthropic":
		if c.AnthropicKey == "" {
			return nil, errors.New("anthropic provider requires ANTHROPIC_API_KEY")
		}
		return NewAnthropic(c.AnthropicKey, model), nil
	case "openai":
		if c.OpenAIKey == "" && strings.Contains(c.OpenAIBaseURL, "api.openai.com") {
			return nil, errors.New("openai provider requires OPENAI_API_KEY")
		}
		m := c.OpenAIModel
		if fallback && c.FallbackModel != "" {
			m = c.FallbackModel
		}
		return NewOpenAI(c.OpenAIKey, m, c.OpenAIBaseURL), nil
	case "mock":
		return Mock{}, nil
	}
	return nil, fmt.Errorf("unknown AI provider %q", name)
}

// CompleteJSON asks the provider for JSON and decodes it into out.
func CompleteJSON(ctx context.Context, p Provider, r Request, out any) error {
	r.System += "\nRespond with a single JSON object and nothing else."
	txt, err := p.Complete(ctx, r)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUpstream, err)
	}
	i, j := strings.Index(txt, "{"), strings.LastIndex(txt, "}")
	if i < 0 || j < i {
		return fmt.Errorf("%w: model returned no JSON object", ErrUpstream)
	}
	if err := json.Unmarshal([]byte(txt[i:j+1]), out); err != nil {
		return fmt.Errorf("%w: decode model JSON: %v", ErrUpstream, err)
	}
	return nil
}
