// Package holmes talks to a HolmesGPT server.
//
// HolmesGPT's HTTP surface has narrowed over time: the older /api/investigate
// endpoint is gone from current releases, and everything now goes through
// POST /api/chat. The bridge therefore poses an investigation as a question,
// with the incident context assembled into the prompt.
package holmes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client calls a HolmesGPT server.
type Client struct {
	baseURL string
	apiKey  string
	model   string
	client  *http.Client
}

// Option configures a Client.
type Option func(*Client)

// WithModel pins the model HolmesGPT should use. Empty means the server's own
// default, which is usually what you want.
func WithModel(model string) Option {
	return func(c *Client) { c.model = model }
}

// WithAPIKey sets a bearer token, for a HolmesGPT deployment behind auth.
func WithAPIKey(key string) Option {
	return func(c *Client) { c.apiKey = key }
}

// WithHTTPClient swaps the HTTP client. Investigations are slow — the default
// allows ten minutes — so a replacement should carry a generous timeout.
func WithHTTPClient(client *http.Client) Option {
	return func(c *Client) { c.client = client }
}

// New builds a client against a HolmesGPT server.
func New(baseURL string, opts ...Option) *Client {
	c := &Client{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		// An investigation runs a chain of tool calls against a live cluster,
		// so minutes is the normal case, not the pathological one.
		client: &http.Client{Timeout: 10 * time.Minute},
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}

// ChatRequest is the body of POST /api/chat.
type ChatRequest struct {
	Ask                    string    `json:"ask"`
	Model                  string    `json:"model,omitempty"`
	Stream                 bool      `json:"stream"`
	ConversationID         string    `json:"conversation_id,omitempty"`
	ConversationHistory    []Message `json:"conversation_history,omitempty"`
	AdditionalSystemPrompt string    `json:"additional_system_prompt,omitempty"`
	RequestSource          string    `json:"request_source,omitempty"`
	UserEmail              string    `json:"user_email,omitempty"`
}

// Message is one turn of a HolmesGPT conversation.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ToolCall records one tool HolmesGPT invoked while answering.
type ToolCall struct {
	ToolName    string `json:"tool_name"`
	Description string `json:"description"`
	Result      any    `json:"result,omitempty"`
}

// ChatResponse is the body HolmesGPT returns from /api/chat.
type ChatResponse struct {
	Analysis            string     `json:"analysis"`
	ToolCalls           []ToolCall `json:"tool_calls"`
	ConversationHistory []Message  `json:"conversation_history,omitempty"`
	Metadata            any        `json:"metadata,omitempty"`
}

// Info is what GET /api/info reports. It is enough to confirm at boot that the
// configured URL really is a HolmesGPT server, that it has a usable model, and
// that its toolsets came up — a Holmes with every toolset failed will answer,
// but only from the prompt, which is worse than useless during an incident.
type Info struct {
	Version  string         `json:"version"`
	Models   []string       `json:"models"`
	Toolsets ToolsetSummary `json:"toolsets_summary"`
}

// ToolsetSummary counts how many of Holmes's toolsets are usable.
type ToolsetSummary struct {
	Total    int `json:"total"`
	Enabled  int `json:"enabled"`
	Failed   int `json:"failed"`
	Disabled int `json:"disabled"`
}

// HasModel reports whether name is one of the models this server can serve.
// An empty name means "the server default", which is always acceptable.
func (i Info) HasModel(name string) bool {
	if name == "" {
		return true
	}

	for _, m := range i.Models {
		if m == name {
			return true
		}
	}

	return false
}

// Ask puts a question to HolmesGPT and returns its analysis.
func (c *Client) Ask(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	if req.Model == "" {
		req.Model = c.model
	}

	if req.RequestSource == "" {
		req.RequestSource = "holmes-bridge"
	}

	// Streaming would need an SSE reader, and the bridge has nobody to stream
	// to: it posts a finished analysis back to incident.io.
	req.Stream = false

	var out ChatResponse
	if err := c.do(ctx, http.MethodPost, "/api/chat", req, &out); err != nil {
		return nil, err
	}

	return &out, nil
}

// Health reports whether the server is ready to answer.
func (c *Client) Health(ctx context.Context) error {
	return c.do(ctx, http.MethodGet, "/healthz", nil, nil)
}

// Info reads the server's advertised version and model.
func (c *Client) Info(ctx context.Context) (Info, error) {
	var out Info
	if err := c.do(ctx, http.MethodGet, "/api/info", nil, &out); err != nil {
		return Info{}, err
	}

	return out, nil
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader

	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("failed to encode the %s request: %w", path, err)
		}

		reader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("failed to build the %s request: %w", path, err)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	req.Header.Set("Accept", "application/json")

	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("holmesgpt %s %s failed: %w", method, path, err)
	}

	defer func() { _ = resp.Body.Close() }()

	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("failed to read the %s response: %w", path, err)
	}

	if resp.StatusCode >= http.StatusBadRequest {
		return &Error{
			Operation:  method + " " + path,
			StatusCode: resp.StatusCode,
			Body:       string(payload),
		}
	}

	if out == nil {
		return nil
	}

	if decodeErr := json.Unmarshal(payload, out); decodeErr != nil {
		return fmt.Errorf("holmesgpt %s returned a body that is not the expected JSON: %w", path, decodeErr)
	}

	return nil
}

// maxResponseBytes bounds how much of a response is read. Analyses are long,
// but not this long, and an unbounded read is a memory hazard.
const maxResponseBytes = 8 << 20
