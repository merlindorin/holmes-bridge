// Package ntfy publishes notifications to an ntfy server.
//
// The bridge uses it to push an investigation's conclusion to a phone, so an
// on-call engineer sees what HolmesGPT found without watching logs or waiting
// to notice an incident update.
package ntfy

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

// DefaultServer is the public ntfy instance.
const DefaultServer = "https://ntfy.sh"

// Priority maps to ntfy's 1–5 scale.
type Priority int

const (
	PriorityMin     Priority = 1
	PriorityLow     Priority = 2
	PriorityDefault Priority = 3
	PriorityHigh    Priority = 4
	PriorityUrgent  Priority = 5
)

// Notification is one message. It is published as JSON to the server root,
// which keeps the topic and every option in one body rather than spread across
// headers that are easy to get subtly wrong.
type Notification struct {
	Topic    string   `json:"topic"`
	Title    string   `json:"title,omitempty"`
	Message  string   `json:"message"`
	Priority Priority `json:"priority,omitempty"`
	Tags     []string `json:"tags,omitempty"`
	Click    string   `json:"click,omitempty"`
	Markdown bool     `json:"markdown,omitempty"`
}

// Client publishes to one ntfy server and topic.
type Client struct {
	server string
	topic  string
	token  string
	user   string
	pass   string
	client *http.Client
}

// Option configures a Client.
type Option func(*Client)

// WithToken authenticates with an ntfy access token (tk_...).
func WithToken(token string) Option {
	return func(c *Client) { c.token = token }
}

// WithBasicAuth authenticates with a username and password, for servers
// configured that way rather than with tokens.
func WithBasicAuth(user, pass string) Option {
	return func(c *Client) { c.user, c.pass = user, pass }
}

// WithHTTPClient replaces the HTTP client, for tests or a custom transport.
func WithHTTPClient(client *http.Client) Option {
	return func(c *Client) { c.client = client }
}

// New builds a client for topic on server. An empty server means ntfy.sh.
func New(server, topic string, opts ...Option) (*Client, error) {
	if strings.TrimSpace(topic) == "" {
		return nil, fmt.Errorf("ntfy: a topic is required")
	}

	c := &Client{
		server: strings.TrimRight(orDefault(server, DefaultServer), "/"),
		topic:  topic,
		// Notifications are a side effect of an investigation, never the point
		// of one. A short timeout keeps a slow ntfy server from holding up the
		// work that actually matters.
		client: &http.Client{Timeout: 10 * time.Second},
	}

	for _, opt := range opts {
		opt(c)
	}

	return c, nil
}

// Topic reports which topic this client publishes to.
func (c *Client) Topic() string { return c.topic }

// Server reports the server this client publishes to.
func (c *Client) Server() string { return c.server }

// Publish sends one notification.
func (c *Client) Publish(ctx context.Context, n Notification) error {
	n.Topic = c.topic

	body, err := json.Marshal(n)
	if err != nil {
		return fmt.Errorf("ntfy: failed to encode the notification: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.server, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("ntfy: failed to build the request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	switch {
	case c.token != "":
		req.Header.Set("Authorization", "Bearer "+c.token)
	case c.user != "":
		req.SetBasicAuth(c.user, c.pass)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("ntfy: publish to %s failed: %w", c.server, err)
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= http.StatusBadRequest {
		payload, _ := io.ReadAll(io.LimitReader(resp.Body, 512))

		return fmt.Errorf("ntfy: publish to %s returned %d: %s%s",
			c.server, resp.StatusCode, strings.TrimSpace(string(payload)),
			hint(resp.StatusCode, c.token != "" || c.user != ""))
	}

	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))

	return nil
}

// hint explains the rejections that are otherwise baffling.
//
// A 401 with no credentials attached reads as "your token is wrong" when the
// real answer is "you did not send one" — and publishing to ntfy generally
// requires a token.
func hint(status int, authenticated bool) string {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		if authenticated {
			return "\n  The token or username was rejected. Check it is valid, and that it has " +
				"write access to this topic."
		}

		return "\n  No credentials were sent, and publishing to ntfy requires them. " +
			"Set --ntfy-token (NTFY_TOKEN) with a token from your ntfy account or server."
	case http.StatusTooManyRequests:
		return "\n  Rate limited. Slow down, or self-host."
	default:
		return ""
	}
}

func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}

	return value
}
