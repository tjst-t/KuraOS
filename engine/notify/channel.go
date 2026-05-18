// Package notify implements notification channels for KuraOS alerts.
//
// DESIGN_PRINCIPLES #6: for ntfy/webhook use net/http; for smtp use net/smtp
// from stdlib; for line_notify/gotify also stdlib http. No third-party SDKs.
// DESIGN_PRINCIPLES #9: NotificationChannel is an interface so tests can mock.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/smtp"
	"strings"
	"time"
)

// Channel is the interface every notification target must implement.
// DESIGN_PRINCIPLES #9: interface for IO so tests can inject a mock.
type Channel interface {
	// Kind returns the channel type identifier ("ntfy", "webhook", etc.).
	Kind() string
	// Send delivers the notification. ctx carries a per-send deadline.
	Send(ctx context.Context, n Notification) error
}

// Notification is the payload passed to every Channel.Send. Title and Body
// are already resolved from the Event by the dispatcher.
type Notification struct {
	Title    string
	Body     string
	Severity string // "info" | "warning" | "critical" | "ok"
	Source   string
	SentAt   time.Time
}

// ─── ntfy ────────────────────────────────────────────────────────────────────

// NtfyConfig is the JSON blob stored in notification_channels.config_json for
// kind="ntfy".
type NtfyConfig struct {
	URL   string `json:"url"`   // e.g. "https://ntfy.sh/my-topic"
	Token string `json:"token"` // optional auth token (from vault)
}

// NtfyChannel sends notifications to an ntfy server via the simple HTTP
// publish API (no third-party SDK — DESIGN_PRINCIPLES #6).
type NtfyChannel struct {
	cfg    NtfyConfig
	client *http.Client
}

func NewNtfyChannel(cfg NtfyConfig) *NtfyChannel {
	return &NtfyChannel{cfg: cfg, client: &http.Client{Timeout: 10 * time.Second}}
}

func (c *NtfyChannel) Kind() string { return "ntfy" }

func (c *NtfyChannel) Send(ctx context.Context, n Notification) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.URL, strings.NewReader(n.Body))
	if err != nil {
		return fmt.Errorf("ntfy: build request: %w", err)
	}
	req.Header.Set("Title", n.Title)
	req.Header.Set("Priority", ntfyPriority(n.Severity))
	req.Header.Set("Tags", n.Source)
	req.Header.Set("Content-Type", "text/plain")
	if c.cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("ntfy: send: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("ntfy: server returned %d", resp.StatusCode)
	}
	return nil
}

func ntfyPriority(severity string) string {
	switch severity {
	case "critical":
		return "urgent"
	case "warning":
		return "high"
	case "ok":
		return "low"
	default:
		return "default"
	}
}

// ─── webhook ─────────────────────────────────────────────────────────────────

// WebhookConfig is the JSON blob for kind="webhook".
type WebhookConfig struct {
	URL    string            `json:"url"`
	Method string            `json:"method,omitempty"` // default POST
	Headers map[string]string `json:"headers,omitempty"`
}

// WebhookChannel sends a JSON POST (or configured method) to a URL.
type WebhookChannel struct {
	cfg    WebhookConfig
	client *http.Client
}

func NewWebhookChannel(cfg WebhookConfig) *WebhookChannel {
	return &WebhookChannel{cfg: cfg, client: &http.Client{Timeout: 10 * time.Second}}
}

func (c *WebhookChannel) Kind() string { return "webhook" }

type webhookPayload struct {
	Title    string `json:"title"`
	Body     string `json:"body"`
	Severity string `json:"severity"`
	Source   string `json:"source"`
	SentAt   string `json:"sent_at"`
}

func (c *WebhookChannel) Send(ctx context.Context, n Notification) error {
	payload, err := json.Marshal(webhookPayload{
		Title:    n.Title,
		Body:     n.Body,
		Severity: n.Severity,
		Source:   n.Source,
		SentAt:   n.SentAt.UTC().Format(time.RFC3339),
	})
	if err != nil {
		return fmt.Errorf("webhook: marshal: %w", err)
	}
	method := c.cfg.Method
	if method == "" {
		method = http.MethodPost
	}
	req, err := http.NewRequestWithContext(ctx, method, c.cfg.URL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("webhook: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range c.cfg.Headers {
		req.Header.Set(k, v)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("webhook: send: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("webhook: server returned %d", resp.StatusCode)
	}
	return nil
}

// ─── smtp ────────────────────────────────────────────────────────────────────

// SMTPConfig is the JSON blob for kind="smtp".
type SMTPConfig struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`     // 587 or 465
	Username string `json:"username"`
	Password string `json:"password"` // from vault at send time
	From     string `json:"from"`
	To       string `json:"to"` // comma-separated
}

// SMTPChannel sends via net/smtp (stdlib, no third-party SDK).
type SMTPChannel struct {
	cfg SMTPConfig
}

func NewSMTPChannel(cfg SMTPConfig) *SMTPChannel {
	return &SMTPChannel{cfg: cfg}
}

func (c *SMTPChannel) Kind() string { return "smtp" }

func (c *SMTPChannel) Send(ctx context.Context, n Notification) error {
	addr := fmt.Sprintf("%s:%d", c.cfg.Host, c.cfg.Port)
	auth := smtp.PlainAuth("", c.cfg.Username, c.cfg.Password, c.cfg.Host)

	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: [KuraOS] %s\r\n\r\n%s\r\n",
		c.cfg.From, c.cfg.To, n.Title, n.Body)

	if err := smtp.SendMail(addr, auth, c.cfg.From, strings.Split(c.cfg.To, ","), []byte(msg)); err != nil {
		return fmt.Errorf("smtp: send: %w", err)
	}
	return nil
}

// ─── line_notify ─────────────────────────────────────────────────────────────

// LineNotifyConfig is the JSON blob for kind="line_notify".
type LineNotifyConfig struct {
	Token string `json:"token"` // from vault
}

// LineNotifyChannel sends via the LINE Notify API (HTTP, no SDK).
type LineNotifyChannel struct {
	cfg    LineNotifyConfig
	client *http.Client
}

func NewLineNotifyChannel(cfg LineNotifyConfig) *LineNotifyChannel {
	return &LineNotifyChannel{cfg: cfg, client: &http.Client{Timeout: 10 * time.Second}}
}

func (c *LineNotifyChannel) Kind() string { return "line_notify" }

func (c *LineNotifyChannel) Send(ctx context.Context, n Notification) error {
	body := fmt.Sprintf("message=[KuraOS] %s\n%s", n.Title, n.Body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://notify-api.line.me/api/notify",
		strings.NewReader(body))
	if err != nil {
		return fmt.Errorf("line_notify: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("line_notify: send: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("line_notify: server returned %d", resp.StatusCode)
	}
	return nil
}

// ─── gotify ──────────────────────────────────────────────────────────────────

// GotifyConfig is the JSON blob for kind="gotify".
type GotifyConfig struct {
	URL   string `json:"url"`   // e.g. "https://push.example.com"
	Token string `json:"token"` // from vault
}

// GotifyChannel sends via the Gotify REST API (HTTP, no SDK).
type GotifyChannel struct {
	cfg    GotifyConfig
	client *http.Client
}

func NewGotifyChannel(cfg GotifyConfig) *GotifyChannel {
	return &GotifyChannel{cfg: cfg, client: &http.Client{Timeout: 10 * time.Second}}
}

func (c *GotifyChannel) Kind() string { return "gotify" }

func (c *GotifyChannel) Send(ctx context.Context, n Notification) error {
	payload := map[string]any{
		"title":    n.Title,
		"message":  n.Body,
		"priority": gotifyPriority(n.Severity),
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("gotify: marshal: %w", err)
	}
	url := strings.TrimRight(c.cfg.URL, "/") + "/message?token=" + c.cfg.Token
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("gotify: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("gotify: send: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("gotify: server returned %d", resp.StatusCode)
	}
	return nil
}

func gotifyPriority(severity string) int {
	switch severity {
	case "critical":
		return 10
	case "warning":
		return 5
	default:
		return 1
	}
}
