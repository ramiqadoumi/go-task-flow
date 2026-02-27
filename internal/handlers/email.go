package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/smtp"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/ramiqadoumi/go-task-flow/internal/domain"
)

// EmailConfig holds SMTP connection details.
type EmailConfig struct {
	Host     string
	Port     int
	From     string
	Username string
	Password string
}

// emailPayload is the expected JSON structure in task.Payload.
type emailPayload struct {
	To      string `json:"to"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

// EmailHandler sends an email via SMTP.
type EmailHandler struct {
	cfg EmailConfig
}

// NewEmailHandler creates an EmailHandler from config.
func NewEmailHandler(cfg EmailConfig) *EmailHandler {
	return &EmailHandler{cfg: cfg}
}

func (h *EmailHandler) TaskType() string { return "email" }

func (h *EmailHandler) Handle(ctx context.Context, task *domain.Task) error {
	ctx, span := otel.Tracer("worker").Start(ctx, "handler.email")
	defer span.End()

	var p emailPayload
	if err := json.Unmarshal(task.Payload, &p); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "invalid payload")
		return fmt.Errorf("invalid email payload: %w", err)
	}
	if p.To == "" {
		err := errors.New("email payload missing required field 'to'")
		span.RecordError(err)
		span.SetStatus(codes.Error, "missing 'to' field")
		return err
	}

	span.SetAttributes(attribute.String("email.to", p.To))

	addr := fmt.Sprintf("%s:%d", h.cfg.Host, h.cfg.Port)
	msg := buildMIME(h.cfg.From, p.To, p.Subject, p.Body)

	var auth smtp.Auth
	if h.cfg.Username != "" {
		auth = smtp.PlainAuth("", h.cfg.Username, h.cfg.Password, h.cfg.Host)
	}

	// Run the blocking SMTP call in a goroutine so we respect ctx cancellation.
	type result struct{ err error }
	done := make(chan result, 1)
	go func() {
		done <- result{err: smtp.SendMail(addr, auth, h.cfg.From, []string{p.To}, msg)}
	}()

	select {
	case res := <-done:
		if res.err != nil {
			span.RecordError(res.err)
			span.SetStatus(codes.Error, "smtp send failed")
			return fmt.Errorf("smtp send to %s: %w", p.To, res.err)
		}
		return nil
	case <-ctx.Done():
		err := fmt.Errorf("email send timed out: %w", ctx.Err())
		span.RecordError(err)
		span.SetStatus(codes.Error, "timeout")
		return err
	}
}

func buildMIME(from, to, subject, body string) []byte {
	msg := fmt.Sprintf(
		"From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s",
		from, to, subject, body,
	)
	return []byte(msg)
}
