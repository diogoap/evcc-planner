// Package openrouter calls a chat completions model through OpenRouter with
// strict JSON Schema output. The forecast extraction and AI mode share it.
package openrouter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/diogoap/evcc-planner/internal/rejected"
)

// Client holds the connection; the base URL receives the API key.
type Client struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
}

// Request is one structured chat completion.
type Request struct {
	Model  string
	System string
	User   string
	// SchemaName and Schema describe the JSON answer (strict mode).
	SchemaName string
	Schema     map[string]any
	MaxTokens  int
	// Where says where the model is chosen, for the hint when no provider
	// supports strict output, e.g. "Settings → Connections".
	Where string
}

// Usage reports model tokens for cost tracking.
type Usage struct {
	Model            string `json:"model"`
	PromptTokens     int    `json:"promptTokens"`
	CompletionTokens int    `json:"completionTokens"`
}

// Complete asks the model and returns its JSON answer (without code
// fences). It only routes to providers that honour every parameter: strict
// structured output plus a token cap. Optional tuning such as temperature is
// left out, because many models (e.g. recent Claude ones) reject it and
// would then match no provider; callers validate the output anyway. Errors
// never contain the key.
func (c *Client) Complete(ctx context.Context, r Request) (string, Usage, error) {
	body := map[string]any{
		"model": r.Model,
		"messages": []map[string]string{
			{"role": "system", "content": r.System},
			{"role": "user", "content": r.User},
		},
		"response_format": map[string]any{
			"type":        "json_schema",
			"json_schema": map[string]any{"name": r.SchemaName, "strict": true, "schema": r.Schema},
		},
		"provider":   map[string]any{"require_parameters": true},
		"max_tokens": r.MaxTokens,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return "", Usage{}, err
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return "", Usage{}, err
	}
	hreq.Header.Set("Content-Type", "application/json")
	hreq.Header.Set("Authorization", "Bearer "+c.APIKey)
	hreq.Header.Set("HTTP-Referer", "https://github.com/diogoap/evcc-planner")
	hreq.Header.Set("X-Title", "EVCC Trip Planner")
	resp, err := c.HTTP.Do(hreq)
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) {
			return "", Usage{}, errors.New("openrouter did not answer in time")
		}
		return "", Usage{}, errors.New("openrouter unreachable")
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", Usage{}, err
	}
	var out struct {
		Model   string `json:"model"`
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	jsonErr := json.Unmarshal(data, &out)
	if resp.StatusCode/100 != 2 || out.Error != nil {
		msg := resp.Status
		if jsonErr == nil && out.Error != nil && out.Error.Message != "" {
			msg = out.Error.Message
		}
		msg = Clip(msg, 300)
		if strings.Contains(strings.ToLower(msg), "no endpoints found") {
			where := r.Where
			if where == "" {
				where = "Settings → Connections"
			}
			msg += " (" + r.Model + " has no provider with strict structured outputs: choose another OpenRouter model under " + where + ")"
		}
		if resp.StatusCode == http.StatusUnauthorized {
			return "", Usage{}, fmt.Errorf("openrouter: %w", &rejected.Error{What: "API key", Status: resp.StatusCode, Message: msg})
		}
		return "", Usage{}, fmt.Errorf("openrouter: %s", msg)
	}
	if jsonErr != nil {
		return "", Usage{}, fmt.Errorf("decode openrouter response: %w", jsonErr)
	}
	usage := Usage{Model: out.Model, PromptTokens: out.Usage.PromptTokens, CompletionTokens: out.Usage.CompletionTokens}
	if len(out.Choices) == 0 {
		return "", usage, errors.New("openrouter returned no choices")
	}
	if out.Choices[0].FinishReason == "length" {
		return "", usage, fmt.Errorf("model output cut off at %d tokens", r.MaxTokens)
	}
	content := strings.TrimSpace(out.Choices[0].Message.Content)
	content = strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(content, "```json"), "```"), "```")
	return strings.TrimSpace(content), usage, nil
}

// Clip shortens s to at most n runes, marking the cut.
func Clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}
