package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramiqadoumi/go-task-flow/internal/domain"
	"github.com/ramiqadoumi/go-task-flow/internal/handlers"
)

func TestWebhookHandler_TaskType(t *testing.T) {
	h := handlers.NewWebhookHandler()
	assert.Equal(t, "webhook", h.TaskType())
}

func TestWebhookHandler_Handle_InvalidJSON(t *testing.T) {
	h := handlers.NewWebhookHandler()
	task := &domain.Task{Payload: []byte("not-json")}

	err := h.Handle(context.Background(), task)
	require.Error(t, err)
}

func TestWebhookHandler_Handle_MissingURL(t *testing.T) {
	h := handlers.NewWebhookHandler()
	task := &domain.Task{Payload: []byte(`{"method":"POST","body":"hello"}`)}

	err := h.Handle(context.Background(), task)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "url")
}

func TestWebhookHandler_Handle_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	h := handlers.NewWebhookHandler()
	task := &domain.Task{
		Payload: []byte(`{"url":"` + srv.URL + `","method":"POST","body":"ping"}`),
	}

	err := h.Handle(context.Background(), task)
	require.NoError(t, err)
}

func TestWebhookHandler_Handle_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	h := handlers.NewWebhookHandler()
	task := &domain.Task{
		Payload: []byte(`{"url":"` + srv.URL + `","method":"GET"}`),
	}

	err := h.Handle(context.Background(), task)
	require.Error(t, err, "status 500 should produce an error")
}

func TestWebhookHandler_Handle_DefaultsMethodToPOST(t *testing.T) {
	var receivedMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	h := handlers.NewWebhookHandler()
	task := &domain.Task{
		Payload: []byte(`{"url":"` + srv.URL + `"}`), // no "method" field
	}

	err := h.Handle(context.Background(), task)
	require.NoError(t, err)
	assert.Equal(t, http.MethodPost, receivedMethod)
}

func TestWebhookHandler_Handle_SetsCustomHeaders(t *testing.T) {
	var receivedHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeader = r.Header.Get("X-Secret")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	h := handlers.NewWebhookHandler()
	task := &domain.Task{
		Payload: []byte(`{"url":"` + srv.URL + `","headers":{"X-Secret":"token123"}}`),
	}

	err := h.Handle(context.Background(), task)
	require.NoError(t, err)
	assert.Equal(t, "token123", receivedHeader)
}
