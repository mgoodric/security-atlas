// Unit tests for the OTel SDK init wiring (slice 121).
//
// These cover the no-op-when-unset property (AC-2), the env-var contract
// (AC-3), and the startup-log shape (AC-4). The end-to-end span/metric
// emission against a real OTel Collector is covered by the integration
// test in this same package.

package otel

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

// TestInit_NoOp_WhenEndpointUnset asserts AC-2: with no
// OTEL_EXPORTER_OTLP_ENDPOINT set, Init returns a Result with
// Enabled=false, no Prometheus handler, and an idempotent shutdown.
// Atlas can boot and serve traffic without any OTel infrastructure.
func TestInit_NoOp_WhenEndpointUnset(t *testing.T) {
	t.Setenv(EnvEndpoint, "")
	t.Setenv(EnvMetricsFallback, "")

	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	res, err := Init(context.Background(), Options{Logger: logger})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if res == nil {
		t.Fatal("Init returned nil result")
	}
	if res.Enabled {
		t.Fatal("Enabled = true; want false when endpoint unset")
	}
	if res.PrometheusHandler != nil {
		t.Fatal("PrometheusHandler must be nil when fallback is off")
	}
	if res.Shutdown == nil {
		t.Fatal("Shutdown must be non-nil in every Result")
	}
	// AC-4 startup-log: disabled-state line names the env-var so an
	// operator grepping the logs gets a pointer.
	out := buf.String()
	if !strings.Contains(out, "opentelemetry: disabled") {
		t.Errorf("startup log missing disabled line: %q", out)
	}
	if !strings.Contains(out, EnvEndpoint) {
		t.Errorf("startup log must mention OTEL_EXPORTER_OTLP_ENDPOINT: %q", out)
	}
	// Idempotent shutdown.
	if err := res.Shutdown(context.Background()); err != nil {
		t.Fatalf("first shutdown: %v", err)
	}
	if err := res.Shutdown(context.Background()); err != nil {
		t.Fatalf("second shutdown: %v", err)
	}
}

// TestServiceName_Default asserts the resolved service-name default.
// OTEL_SERVICE_NAME is the standard knob; we surface the resolved value
// to the startup log.
func TestServiceName_Default(t *testing.T) {
	t.Setenv(EnvServiceName, "")
	if got := serviceName(); got != DefaultServiceName {
		t.Errorf("serviceName() = %q; want %q", got, DefaultServiceName)
	}
}

// TestServiceName_OverrideViaEnv asserts the env-var override.
func TestServiceName_OverrideViaEnv(t *testing.T) {
	t.Setenv(EnvServiceName, "atlas-staging")
	if got := serviceName(); got != "atlas-staging" {
		t.Errorf("serviceName() = %q; want %q", got, "atlas-staging")
	}
}

// TestParseFloat covers the SAMPLER_ARG parser. Clamped to (0, 1].
func TestParseFloat(t *testing.T) {
	cases := []struct {
		in       string
		fallback float64
		want     float64
	}{
		{"", 0.1, 0.1},
		{"0.5", 0.1, 0.5},
		{"1", 0.1, 1.0},
		{"0", 0.1, 0.1},       // 0 falls back (no traces is hostile to debugging)
		{"-0.5", 0.1, 0.1},    // negative falls back
		{"1.5", 0.1, 0.1},     // out-of-range falls back
		{"abc", 0.1, 0.1},     // garbage falls back
		{"0.001", 0.1, 0.001}, // small valid value
	}
	for _, tc := range cases {
		got := parseFloat(tc.in, tc.fallback)
		if got != tc.want {
			t.Errorf("parseFloat(%q, %v) = %v; want %v", tc.in, tc.fallback, got, tc.want)
		}
	}
}

// TestNewPropagator asserts the W3C tracecontext + baggage composite.
// Any OTel-aware peer expects this.
func TestNewPropagator(t *testing.T) {
	p := newPropagator()
	if p == nil {
		t.Fatal("newPropagator returned nil")
	}
	// Composite has both fields registered; assertion via Fields().
	fields := p.Fields()
	hasTraceparent := false
	hasBaggage := false
	for _, f := range fields {
		switch strings.ToLower(f) {
		case "traceparent":
			hasTraceparent = true
		case "baggage":
			hasBaggage = true
		}
	}
	if !hasTraceparent {
		t.Error("propagator missing W3C traceparent header")
	}
	if !hasBaggage {
		t.Error("propagator missing W3C baggage header")
	}
}
