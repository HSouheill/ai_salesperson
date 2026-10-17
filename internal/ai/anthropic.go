package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

type Anthropic struct {
	key, model string
	http       *http.Client
	baseURL    string
}

func NewAnthropic(key, model string) *Anthropic {
	return &Anthropic{key: key, model: model, http: &http.Client{Timeout: 90 * time.Second}, baseURL: "https://api.anthropic.com"}
}

func (a *Anthropic) Complete(ctx context.Context, r Request) (string, error) {
	if r.MaxTokens == 0 {
		r.MaxTokens = 2048
	}
	payload := map[string]any{
		"model":      a.model,
		"max_tokens": r.MaxTokens,
		"system":     r.System,
		"messages":   []map[string]string{{"role": "user", "content": r.Prompt}},
	}
	if r.WebSearch {
		max := r.MaxSearches
		if max <= 0 {
			max = 5
		}
		// Claude's server-side web search tool: Anthropic itself issues and
		// executes the searches (no client-side tool loop to drive), and
		// returns the synthesized answer as ordinary "text" content blocks
		// alongside the search-tool blocks we skip below.
		payload["tools"] = []map[string]any{{"type": "web_search_20250305", "name": "web_search", "max_uses": max}}
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-api-key", a.key)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := a.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		return "", &StatusError{Provider: "anthropic", Code: resp.StatusCode, Body: truncate(string(raw), 300)}
	}
	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	var sb strings.Builder
	for _, c := range out.Content {
		if c.Type == "text" {
			sb.WriteString(c.Text)
		}
	}
	return sb.String(), nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
