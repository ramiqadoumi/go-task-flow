package domain_test

import (
	"testing"

	"github.com/ramiqadoumi/go-task-flow/internal/domain"
)

func TestStatusConstants(t *testing.T) {
	tests := []struct {
		status domain.Status
		want   string
	}{
		{domain.StatusPending, "PENDING"},
		{domain.StatusQueued, "QUEUED"},
		{domain.StatusRunning, "RUNNING"},
		{domain.StatusDone, "DONE"},
		{domain.StatusFailed, "FAILED"},
		{domain.StatusRetrying, "RETRYING"},
		{domain.StatusDead, "DEAD"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if string(tt.status) != tt.want {
				t.Errorf("Status value = %q, want %q", tt.status, tt.want)
			}
		})
	}
}

func TestIsTerminal_TerminalStates(t *testing.T) {
	for _, s := range []domain.Status{domain.StatusDone, domain.StatusDead, domain.StatusFailed} {
		t.Run(string(s), func(t *testing.T) {
			if !s.IsTerminal() {
				t.Errorf("IsTerminal(%q) = false, want true", s)
			}
		})
	}
}

func TestIsTerminal_NonTerminalStates(t *testing.T) {
	for _, s := range []domain.Status{
		domain.StatusPending, domain.StatusQueued,
		domain.StatusRunning, domain.StatusRetrying,
	} {
		t.Run(string(s), func(t *testing.T) {
			if s.IsTerminal() {
				t.Errorf("IsTerminal(%q) = true, want false", s)
			}
		})
	}
}
