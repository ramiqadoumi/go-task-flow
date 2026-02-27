package telemetry

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// ─── API Gateway ─────────────────────────────────────────────────────────────

	APITasksSubmitted = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "taskflow",
		Subsystem: "api",
		Name:      "tasks_submitted_total",
		Help:      "Total tasks submitted through the API gateway.",
	}, []string{"type"})

	// ─── Worker ──────────────────────────────────────────────────────────────────

	WorkerTasksProcessed = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "taskflow",
		Subsystem: "worker",
		Name:      "tasks_processed_total",
		Help:      "Total tasks processed, labelled by worker_type and terminal status.",
	}, []string{"worker_type", "status"})

	WorkerTasksInFlight = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "taskflow",
		Subsystem: "worker",
		Name:      "tasks_inflight",
		Help:      "Tasks currently being executed.",
	}, []string{"worker_type"})

	WorkerTaskDurationSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "taskflow",
		Subsystem: "worker",
		Name:      "task_duration_seconds",
		Help:      "End-to-end task execution time in seconds.",
		Buckets:   []float64{0.01, 0.05, 0.1, 0.5, 1, 2, 5, 10, 30},
	}, []string{"worker_type"})

	WorkerRetriesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "taskflow",
		Subsystem: "worker",
		Name:      "retries_total",
		Help:      "Total retry attempts.",
	}, []string{"worker_type"})

	WorkerDLQTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "taskflow",
		Subsystem: "worker",
		Name:      "dlq_total",
		Help:      "Total tasks forwarded to the dead-letter queue.",
	}, []string{"worker_type"})

	// ─── Dispatcher ──────────────────────────────────────────────────────────────

	DispatcherTasksRouted = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "taskflow",
		Subsystem: "dispatcher",
		Name:      "tasks_routed_total",
		Help:      "Total tasks routed to per-type worker topics.",
	}, []string{"task_type"})

	DispatcherDLQTotal = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "taskflow",
		Subsystem: "dispatcher",
		Name:      "dlq_total",
		Help:      "Total tasks sent to DLQ by the dispatcher (malformed or no type).",
	})

	DispatcherRateLimitedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "taskflow",
		Subsystem: "dispatcher",
		Name:      "rate_limited_total",
		Help:      "Total tasks rejected by the rate limiter.",
	})
)
