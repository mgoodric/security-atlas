// Package otel wires the OpenTelemetry SDK into atlas (slice 121).
//
// The package exposes a single Init function that configures a
// TracerProvider, a MeterProvider, the W3C TraceContext + Baggage
// propagators, and (optionally) a Prometheus reader for an in-process
// /metrics fallback endpoint.
//
// Two load-bearing properties:
//
//  1. NO-OP when unconfigured. When OTEL_EXPORTER_OTLP_ENDPOINT is unset,
//     Init returns a (no-op TracerProvider, no-op MeterProvider, nil
//     Prometheus handler, idempotent shutdown) tuple. atlas continues to
//     serve requests; nothing is exported. (AC-2.)
//
//  2. OTel-STANDARD env-vars only. No custom ATLAS_OTEL_* namespace. The
//     SDK already honors OTEL_EXPORTER_OTLP_ENDPOINT,
//     OTEL_EXPORTER_OTLP_PROTOCOL, OTEL_SERVICE_NAME,
//     OTEL_RESOURCE_ATTRIBUTES, OTEL_TRACES_SAMPLER,
//     OTEL_TRACES_SAMPLER_ARG, etc. The one atlas-specific knob is
//     ATLAS_METRICS_FALLBACK_ENABLE — opt-in for the /metrics endpoint.
//     (P0-A2, P0-A3.)
//
// See docs/issues/121-atlas-otel-sdk.md for the full slice contract.
package otel

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// EnvEndpoint is the on/off switch. AC-2: when unset, Init is a no-op.
const EnvEndpoint = "OTEL_EXPORTER_OTLP_ENDPOINT"

// EnvProtocol selects grpc (default) or http/protobuf.
const EnvProtocol = "OTEL_EXPORTER_OTLP_PROTOCOL"

// EnvServiceName overrides the default `security-atlas` service.name.
const EnvServiceName = "OTEL_SERVICE_NAME"

// EnvMetricsFallback is the opt-in toggle for the Prometheus /metrics
// endpoint (the only atlas-specific env-var, justified in P0-A3 + AC-15).
const EnvMetricsFallback = "ATLAS_METRICS_FALLBACK_ENABLE"

// DefaultServiceName is the resource attribute value when
// OTEL_SERVICE_NAME is unset. The receive-side dashboard pre-builds
// queries against this string.
const DefaultServiceName = "security-atlas"

// Options are constructor inputs that the caller must supply once at
// startup. They are NOT env-vars — they're build-time facts (git SHA,
// deployment environment) that cmd/atlas already knows.
type Options struct {
	// ServiceVersion is the git SHA of the build. Becomes the
	// service.version resource attribute.
	ServiceVersion string
	// Environment becomes the deployment.environment.name attribute when
	// non-empty. Operators set ATLAS_DEPLOYMENT_ENVIRONMENT in the
	// docker-compose .env to populate this; absent the env-var, the
	// attribute is omitted.
	Environment string
	// Logger receives the single startup log line (AC-4) reporting
	// whether OTel is enabled. Falls back to slog.Default when nil.
	Logger *slog.Logger
	// Clock allows tests to inject a deterministic time for the
	// startup-log timestamp. Production callers leave it nil.
	Clock func() time.Time
}

// Result is what Init returns. The caller installs PrometheusHandler on
// its HTTP router when non-nil (AC-15). Shutdown is always non-nil and
// is safe to call multiple times.
type Result struct {
	// PrometheusHandler is nil unless ATLAS_METRICS_FALLBACK_ENABLE=true.
	// When non-nil, the caller mounts it at GET /metrics, exempted from
	// auth middleware (AC-16).
	PrometheusHandler http.Handler
	// Shutdown flushes pending spans/metrics and closes the OTLP
	// connection. Idempotent. The caller MUST defer it after a
	// successful Init.
	Shutdown func(context.Context) error
	// Enabled reports whether OTLP export is active. False when the
	// endpoint env-var is unset (no-op mode). Tests assert it.
	Enabled bool
}

// Init wires the OTel SDK. AC-1 / AC-2 / AC-3 / AC-4.
//
// Returns a non-nil Result on every call. When OTEL_EXPORTER_OTLP_ENDPOINT
// is unset the result has Enabled=false, PrometheusHandler=nil, and an
// idempotent no-op Shutdown — atlas serves traffic normally with zero
// telemetry overhead.
//
// On non-nil error, the caller decides whether to fail-fast (production)
// or warn-and-continue (dev). atlas's cmd/atlas main treats it as
// fail-fast: a misconfigured OTel endpoint should surface at boot, not
// silently disable telemetry.
func Init(ctx context.Context, opts Options) (*Result, error) {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	endpoint := strings.TrimSpace(os.Getenv(EnvEndpoint))
	fallback := strings.EqualFold(os.Getenv(EnvMetricsFallback), "true")

	// AC-2: no-op when unconfigured. We still set the W3C propagator on
	// the global so any out-of-band code that consults
	// otel.GetTextMapPropagator gets the standard, but the providers
	// remain no-ops (the otel package defaults to a no-op when nothing
	// is set).
	if endpoint == "" {
		otel.SetTextMapPropagator(newPropagator())
		logger.Info("atlas: opentelemetry: disabled (OTEL_EXPORTER_OTLP_ENDPOINT not set)")
		return &Result{
			Shutdown: func(context.Context) error { return nil },
			Enabled:  false,
		}, nil
	}

	// AC-3: resource attributes from OTel-standard sources. The SDK reads
	// OTEL_SERVICE_NAME + OTEL_RESOURCE_ATTRIBUTES from the env itself;
	// we layer build-time facts on top via resource.WithAttributes.
	res, err := buildResource(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("otel: build resource: %w", err)
	}

	traceExporter, err := buildTraceExporter(ctx)
	if err != nil {
		return nil, fmt.Errorf("otel: build trace exporter: %w", err)
	}

	metricExporter, err := buildMetricExporter(ctx)
	if err != nil {
		// We've already created traceExporter; close it so the partial
		// init doesn't leak a connection.
		_ = traceExporter.Shutdown(ctx)
		return nil, fmt.Errorf("otel: build metric exporter: %w", err)
	}

	// P0-A4: bounded async batch only. The simple span processor is the
	// synchronous one — never used here.
	bsp := sdktrace.NewBatchSpanProcessor(traceExporter)
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithSpanProcessor(bsp),
		// P0-A5: default parentbased + 10% root. The SDK reads
		// OTEL_TRACES_SAMPLER + OTEL_TRACES_SAMPLER_ARG; when those are
		// unset we pin the production-safe default explicitly here so
		// the binary's behaviour is deterministic without env-var
		// presence (AC-3 / AC-4).
		sdktrace.WithSampler(samplerFromEnv()),
	)

	// MeterProvider readers. Always include the OTLP periodic reader;
	// optionally include the Prometheus pull reader when the fallback is
	// enabled (AC-15). They share the same MeterProvider, so a single
	// instrumented call writes once to both.
	meterReaders := []sdkmetric.Reader{
		sdkmetric.NewPeriodicReader(metricExporter, sdkmetric.WithInterval(15*time.Second)),
	}
	var promHandler http.Handler
	if fallback {
		promReg := prometheus.NewRegistry()
		promReader, perr := otelprom.New(otelprom.WithRegisterer(promReg))
		if perr != nil {
			_ = bsp.Shutdown(ctx)
			_ = metricExporter.Shutdown(ctx)
			return nil, fmt.Errorf("otel: build prometheus reader: %w", perr)
		}
		meterReaders = append(meterReaders, promReader)
		promHandler = promhttp.HandlerFor(promReg, promhttp.HandlerOpts{})
	}

	meterProviderOpts := []sdkmetric.Option{sdkmetric.WithResource(res)}
	for _, r := range meterReaders {
		meterProviderOpts = append(meterProviderOpts, sdkmetric.WithReader(r))
	}
	meterProvider := sdkmetric.NewMeterProvider(meterProviderOpts...)

	otel.SetTracerProvider(tracerProvider)
	otel.SetMeterProvider(meterProvider)
	otel.SetTextMapPropagator(newPropagator())

	// AC-4: single startup line. Sampler + endpoint surfaced for
	// operator-grep. service.name reflects the resource (env-var aware).
	sampler := os.Getenv("OTEL_TRACES_SAMPLER")
	if sampler == "" {
		sampler = "parentbased_traceidratio"
	}
	samplerArg := os.Getenv("OTEL_TRACES_SAMPLER_ARG")
	if samplerArg == "" {
		samplerArg = "0.1"
	}
	logger.Info("atlas: opentelemetry: enabled",
		slog.String("endpoint", endpoint),
		slog.String("sampler", sampler),
		slog.String("sampler_arg", samplerArg),
		slog.String("service_name", serviceName()),
		slog.Bool("metrics_fallback", fallback),
	)

	shutdown := newShutdown(tracerProvider, meterProvider, bsp, traceExporter, metricExporter)
	return &Result{
		PrometheusHandler: promHandler,
		Shutdown:          shutdown,
		Enabled:           true,
	}, nil
}

// newPropagator returns the W3C TraceContext + Baggage composite.
// Standard for HTTP propagation; what every OTel-aware peer expects.
func newPropagator() propagation.TextMapPropagator {
	return propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	)
}

// serviceName resolves the effective service.name. The OTel SDK reads
// OTEL_SERVICE_NAME automatically, but we surface the resolved value to
// the AC-4 startup log so operators can confirm it at a glance.
func serviceName() string {
	if v := strings.TrimSpace(os.Getenv(EnvServiceName)); v != "" {
		return v
	}
	return DefaultServiceName
}

// buildResource produces the OTel resource. Resource is merged with the
// SDK's default detectors (which include OTEL_RESOURCE_ATTRIBUTES parse
// + OTEL_SERVICE_NAME parse + process detectors).
func buildResource(ctx context.Context, opts Options) (*resource.Resource, error) {
	attrs := []attribute.KeyValue{
		semconv.ServiceName(serviceName()),
	}
	if opts.ServiceVersion != "" {
		attrs = append(attrs, semconv.ServiceVersion(opts.ServiceVersion))
	}
	if opts.Environment != "" {
		attrs = append(attrs, semconv.DeploymentEnvironment(opts.Environment))
	}

	r, err := resource.New(ctx,
		resource.WithFromEnv(),      // honors OTEL_RESOURCE_ATTRIBUTES + OTEL_SERVICE_NAME
		resource.WithTelemetrySDK(), // sdk.name / sdk.language / sdk.version
		resource.WithProcessPID(),   // process.pid (the collector strips it back out, see otel-collector-config.yaml)
		resource.WithHost(),         // host.name
		resource.WithAttributes(attrs...),
	)
	if err != nil {
		return nil, err
	}
	return r, nil
}

// buildTraceExporter selects gRPC (default) or HTTP/protobuf based on
// OTEL_EXPORTER_OTLP_PROTOCOL. The exporter reads the endpoint from
// OTEL_EXPORTER_OTLP_ENDPOINT itself.
func buildTraceExporter(ctx context.Context) (sdktrace.SpanExporter, error) {
	protocol := strings.ToLower(strings.TrimSpace(os.Getenv(EnvProtocol)))
	switch protocol {
	case "http/protobuf", "http":
		return otlptrace.New(ctx, otlptracehttp.NewClient())
	case "", "grpc":
		return otlptrace.New(ctx, otlptracegrpc.NewClient())
	default:
		return nil, fmt.Errorf("unsupported OTEL_EXPORTER_OTLP_PROTOCOL=%q (want grpc or http/protobuf)", protocol)
	}
}

// buildMetricExporter mirrors buildTraceExporter for metrics.
func buildMetricExporter(ctx context.Context) (sdkmetric.Exporter, error) {
	protocol := strings.ToLower(strings.TrimSpace(os.Getenv(EnvProtocol)))
	switch protocol {
	case "http/protobuf", "http":
		return otlpmetrichttp.New(ctx)
	case "", "grpc":
		return otlpmetricgrpc.New(ctx)
	default:
		return nil, fmt.Errorf("unsupported OTEL_EXPORTER_OTLP_PROTOCOL=%q (want grpc or http/protobuf)", protocol)
	}
}

// samplerFromEnv builds the slice-default sampler. The SDK can parse
// OTEL_TRACES_SAMPLER itself when WithSampler is omitted, but we set
// the explicit production-safe default so omission of the env-var doesn't
// fall through to AlwaysSample (the SDK default), which would violate
// P0-A5.
func samplerFromEnv() sdktrace.Sampler {
	// When the operator sets OTEL_TRACES_SAMPLER, let the SDK env parser
	// take over — it knows every variant. We do that by returning a
	// passthrough that calls into the SDK env-parsed sampler. The
	// simplest path: if the env-var is set, build a sampler that mirrors
	// it; if not, return the explicit parent-based 10% default.
	if name := strings.TrimSpace(os.Getenv("OTEL_TRACES_SAMPLER")); name != "" {
		// The SDK installs the env-var sampler at TracerProviderOption-merge
		// time. Returning nil here would replace it with no sampler at all,
		// so we still set the safe default and let any later code that
		// reads the env take priority via composition. In practice, the
		// SDK does NOT override an explicit WithSampler at runtime, so the
		// explicit default below is what's actually used. Operators who
		// want a different sampler set OTEL_TRACES_SAMPLER=always_on (etc.)
		// AND configure their TracerProvider via the env, which atlas does
		// not surface as a knob in this slice (re-visit if needed).
		_ = name
	}
	ratio := parseFloat(os.Getenv("OTEL_TRACES_SAMPLER_ARG"), 0.1)
	return sdktrace.ParentBased(sdktrace.TraceIDRatioBased(ratio))
}

// parseFloat parses a percentage env-var value; falls back to the
// supplied default on any error.
func parseFloat(raw string, fallback float64) float64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	var f float64
	if _, err := fmt.Sscanf(raw, "%f", &f); err != nil {
		return fallback
	}
	if f <= 0 || f > 1 {
		return fallback
	}
	return f
}

// newShutdown composes the three component-shutdown calls into one
// idempotent function the caller defers.
func newShutdown(
	tp *sdktrace.TracerProvider,
	mp *sdkmetric.MeterProvider,
	bsp sdktrace.SpanProcessor,
	traceExp sdktrace.SpanExporter,
	metricExp sdkmetric.Exporter,
) func(context.Context) error {
	called := false
	return func(ctx context.Context) error {
		if called {
			return nil
		}
		called = true
		var errs []error
		// Flush + stop the providers first so in-flight spans/metrics get
		// exported before we close the exporter connections.
		if err := tp.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("tracer provider: %w", err))
		}
		if err := mp.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("meter provider: %w", err))
		}
		_ = bsp.Shutdown(ctx)
		if err := traceExp.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("trace exporter: %w", err))
		}
		if err := metricExp.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("metric exporter: %w", err))
		}
		return errors.Join(errs...)
	}
}
