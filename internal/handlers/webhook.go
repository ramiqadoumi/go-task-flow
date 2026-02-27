package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/ramiqadoumi/go-task-flow/internal/domain"
)

// webhookPayload is the expected JSON structure in task.Payload.
type webhookPayload struct {
	URL     string            `json:"url"`
	Method  string            `json:"method"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

// WebhookHandler makes an outbound HTTP call.
type WebhookHandler struct {
	client *http.Client
}

// NewWebhookHandler creates a WebhookHandler.
func NewWebhookHandler() *WebhookHandler {
	return &WebhookHandler{
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

func (h *WebhookHandler) TaskType() string { return "webhook" }

func (h *WebhookHandler) Handle(ctx context.Context, task *domain.Task) error {
	ctx, span := otel.Tracer("worker").Start(ctx, "handler.webhook")
	defer span.End()

	var p webhookPayload
	if err := json.Unmarshal(task.Payload, &p); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "invalid payload")
		return fmt.Errorf("invalid webhook payload: %w", err)
	}
	if p.URL == "" {
		err := errors.New("webhook payload missing required field 'url'")
		span.RecordError(err)
		span.SetStatus(codes.Error, "missing 'url' field")
		return err
	}
	if p.Method == "" {
		p.Method = http.MethodPost
	}

	span.SetAttributes(
		attribute.String("webhook.url", p.URL),
		attribute.String("webhook.method", p.Method),
	)

	var bodyReader io.Reader
	if p.Body != "" {
		bodyReader = strings.NewReader(p.Body)
	}

	req, err := http.NewRequestWithContext(ctx, p.Method, p.URL, bodyReader)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "build request failed")
		return fmt.Errorf("build webhook request: %w", err)
	}

	for k, v := range p.Headers {
		req.Header.Set(k, v)
	}

	resp, err := h.client.Do(req)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "http call failed")
		return fmt.Errorf("webhook call to %s: %w", p.URL, err)
	}
	defer resp.Body.Close()

	span.SetAttributes(attribute.Int("http.status_code", resp.StatusCode))
	if resp.StatusCode >= http.StatusBadRequest {
		err := fmt.Errorf("webhook %s returned status %d", p.URL, resp.StatusCode)
		span.RecordError(err)
		span.SetStatus(codes.Error, "bad status code")
		return err
	}
	return nil
}
