package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	// RequestsTotal counts MaintenanceRequests observed, by mode and target type.
	RequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "maintenance_requests_total",
			Help: "Total number of MaintenanceRequests observed, by mode and target type.",
		},
		[]string{"mode", "target_type"},
	)

	// SuccessTotal counts MaintenanceRequests that completed successfully.
	SuccessTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "maintenance_success_total",
		Help: "Total number of MaintenanceRequests that completed successfully.",
	})

	// FailureTotal counts MaintenanceRequests that failed, by reason.
	FailureTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "maintenance_failure_total",
			Help: "Total number of MaintenanceRequests that failed, by reason.",
		},
		[]string{"reason"},
	)

	// DrainDurationSeconds observes per-node drain duration, by result.
	DrainDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "drain_duration_seconds",
			Help:    "Per-node drain duration in seconds, by result.",
			Buckets: prometheus.ExponentialBuckets(5, 2, 10),
		},
		[]string{"result"},
	)

	// PreflightFailuresTotal counts failed preflight checks, by check code.
	PreflightFailuresTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "preflight_failures_total",
			Help: "Total number of failed preflight checks, by check code.",
		},
		[]string{"check"},
	)

	// BlockedDrainsTotal counts blocked drains, by block reason.
	BlockedDrainsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "blocked_drains_total",
			Help: "Total number of blocked drains, by block reason.",
		},
		[]string{"reason"},
	)

	// ActiveMaintenances reports requests currently in a non-terminal phase.
	ActiveMaintenances = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "active_maintenances",
		Help: "Number of MaintenanceRequests currently in a non-terminal phase.",
	})
)

var collectors = []prometheus.Collector{
	RequestsTotal,
	SuccessTotal,
	FailureTotal,
	DrainDurationSeconds,
	PreflightFailuresTotal,
	BlockedDrainsTotal,
	ActiveMaintenances,
}

// Register adds all collectors to the controller-runtime metrics registry,
// which the manager exposes on its /metrics endpoint. Call exactly once.
func Register() {
	ctrlmetrics.Registry.MustRegister(collectors...)
}
