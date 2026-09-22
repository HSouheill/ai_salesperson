package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

// OpenAI talks to any OpenAI-compatible chat-completions server.
type OpenAI struct {
	key, model, base string
	http             *http.Client
}

func NewOpenAI(key, model, base string) *OpenAI {
	return &OpenAI{key: key, model: model, base: strings.TrimRight(base, "/"), http: &http.Client{Timeout: 90 * time.Second}}
}

func (o *OpenAI) Complete(ctx context.Context, r Request) (string, error) {
	if r.MaxTokens == 0 {
		r.MaxTokens = 2048
	}
	body := map[string]any{
		"model": o.model,
		"messages": []map[string]string{
			{"role": "system", "content": r.System}, {"role": "user", "content": r.Prompt},
		},
	}
	// api.openai.com renamed the limit parameter; compatible servers still use max_tokens.
	if strings.Contains(o.base, "api.openai.com") {
		body["max_completion_tokens"] = r.MaxTokens
	} else {
		body["max_tokens"] = r.MaxTokens
	}
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.base+"/chat/completions", bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if o.key != "" {
		req.Header.Set("Authorization", "Bearer "+o.key)
	}
	resp, err := o.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		return "", &StatusError{Provider: "openai", Code: resp.StatusCode, Body: truncate(string(raw), 300)}
	}
	var out struct {
		Choices []struct {
			Message struct{ Content string } `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	if len(out.Choices) == 0 {
		return "", errors.New("openai: empty response")
	}
	return out.Choices[0].Message.Content, nil
}
