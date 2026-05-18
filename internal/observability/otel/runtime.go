// Go runtime metrics (slice 121, Phase 5 — AC-14).
//
// Wires go.opentelemetry.io/contrib/instrumentation/runtime so atlas
// auto-emits the standard Go runtime metrics:
//
//   runtime.go.goroutines
//   runtime.go.gc.pause_total_ns
//   runtime.go.mem.heap_alloc
//   runtime.go.mem.heap_inuse
//   runtime.go.mem.lookups
//   runtime.go.cgo.calls
//   runtime.uptime
//
// Default collection interval 15s (the contrib package's documented
// default). The metrics flow through the same MeterProvider OTel.Init
// installed, so they get exported via OTLP push and the Prometheus
// fallback simultaneously when both are enabled.

package otel

import (
	"fmt"
	"time"

	runtimeinstrument "go.opentelemetry.io/contrib/instrumentation/runtime"
)

// DefaultRuntimeMetricsInterval matches the contrib package default.
const DefaultRuntimeMetricsInterval = 15 * time.Second

// StartRuntimeMetrics begins emitting Go runtime metrics. Safe to call
// after Init regardless of whether OTel is enabled — when the global
// MeterProvider is the no-op (AC-2), this call is itself a no-op
// (metrics get recorded against the no-op provider and discarded).
//
// Returns the descriptive error from the contrib package so the caller
// can log it; it does NOT fail-fast atlas startup (runtime metrics are
// nice-to-have, not load-bearing).
func StartRuntimeMetrics() error {
	if err := runtimeinstrument.Start(
		runtimeinstrument.WithMinimumReadMemStatsInterval(DefaultRuntimeMetricsInterval),
	); err != nil {
		return fmt.Errorf("otel: start runtime metrics: %w", err)
	}
	return nil
}
