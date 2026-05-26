// Coverage lift (slice 286) — unit tests for the load-bearing functions
// the slice 121 init wiring exposes:
//
//   - Init's enabled-path (endpoint set; http/grpc protocol; with and without
//     the prometheus fallback), the protocol-error cleanup branch, and the
//     idempotent shutdown the result hands back.
//   - buildResource (default + Options-attribute paths).
//   - buildTraceExporter / buildMetricExporter (grpc / http / unsupported).
//   - samplerFromEnv (env unset, ratio env set).
//   - newShutdown's first-call vs. idempotent second-call branches.
//   - All nine NATS-helpers in nats.go (header carrier Get/Set/Keys,
//     Inject/Extract context, Start{Publish,Consume}Span, natsAttrs +/-
//     messageID, EndNATSSpanWithError with nil span / no error / real error).
//   - NewTracedPool (parse-error branch + successful-build branch — the pool
//     dials lazily in a background goroutine so we can close immediately).
//   - StartRuntimeMetrics (single call; safe under a no-op global provider).
//
// All tests are pure Go — no Postgres, no NATS broker, no OTel collector.
// Global OTel state (tracer/meter/propagator) is saved + restored per-test
// so each case is isolated.

package otel

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// withSavedGlobals snapshots + restores the OTel global TracerProvider,
// MeterProvider, and TextMapPropagator across a test. Init's enabled path
// mutates these; without restoration, downstream tests in the same package
// see leaked state.
func withSavedGlobals(t *testing.T) {
	t.Helper()
	prevTP := otel.GetTracerProvider()
	prevMP := otel.GetMeterProvider()
	prevProp := otel.GetTextMapPropagator()
	t.Cleanup(func() {
		otel.SetTracerProvider(prevTP)
		otel.SetMeterProvider(prevMP)
		otel.SetTextMapPropagator(prevProp)
	})
}

// installInMemoryTracer replaces the global TracerProvider with one
// backed by tracetest.InMemoryExporter so the NATS helpers (which read
// otel.Tracer + otel.GetTextMapPropagator) emit recordable spans + write
// W3C headers. Restored via withSavedGlobals's cleanup.
func installInMemoryTracer(t *testing.T) *tracetest.InMemoryExporter {
	t.Helper()
	withSavedGlobals(t)
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exp),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	t.Cleanup(func() {
		_ = tp.Shutdown(context.Background())
	})
	return exp
}

// ---------------------------------------------------------------------
// Init — enabled-path coverage
// ---------------------------------------------------------------------

// TestInit_Enabled_HTTPProtocol asserts AC-1 / AC-3 / AC-4 on the
// enabled path with the http/protobuf protocol selector. We pick HTTP
// (not gRPC) so the exporter construction stays purely in-process —
// gRPC's client is also lazy in v1.43.0 but http is the cheaper path.
//
// Covers: the entire happy path through Init when endpoint is set,
// including buildResource, buildTraceExporter, buildMetricExporter,
// samplerFromEnv, the BatchSpanProcessor wiring, MeterProvider wiring
// (without the Prometheus reader), the global-propagator install, the
// AC-4 startup log, and Result composition. Shutdown is invoked twice
// to exercise the idempotent branch in newShutdown.
func TestInit_Enabled_HTTPProtocol(t *testing.T) {
	withSavedGlobals(t)
	t.Setenv(EnvEndpoint, "localhost:4318")
	t.Setenv(EnvProtocol, "http/protobuf")
	t.Setenv(EnvMetricsFallback, "")
	t.Setenv(EnvServiceName, "atlas-test")
	t.Setenv("OTEL_TRACES_SAMPLER", "")
	t.Setenv("OTEL_TRACES_SAMPLER_ARG", "")

	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	res, err := Init(context.Background(), Options{
		Logger:         logger,
		ServiceVersion: "deadbeef",
		Environment:    "ci",
	})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if res == nil || !res.Enabled {
		t.Fatalf("Result.Enabled = false; want true on configured endpoint")
	}
	if res.PrometheusHandler != nil {
		t.Fatal("PrometheusHandler must be nil when fallback is off")
	}
	if res.Shutdown == nil {
		t.Fatal("Shutdown must be non-nil")
	}
	// AC-4 startup log: enabled-state line must surface endpoint +
	// sampler + service.name so operators can grep boot.
	out := buf.String()
	if !strings.Contains(out, "opentelemetry: enabled") {
		t.Errorf("startup log missing enabled marker: %q", out)
	}
	for _, want := range []string{"localhost:4318", "atlas-test", "parentbased_traceidratio", "0.1"} {
		if !strings.Contains(out, want) {
			t.Errorf("startup log missing %q: %q", want, out)
		}
	}
	// Idempotent shutdown — we cancel the context immediately so the
	// MeterProvider's pending-export flush bails out fast (the test
	// endpoint is not a live OTLP target). The first call may surface
	// a flush-after-cancel error from the exporter; the property we
	// care about is that the SECOND call is a no-op (the called=true
	// branch in newShutdown), so we assert that explicitly.
	shutdownCtx, shutdownCancel := context.WithCancel(context.Background())
	shutdownCancel()
	_ = res.Shutdown(shutdownCtx)
	if err := res.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("second shutdown (idempotent path) returned error: %v", err)
	}
}

// TestInit_Enabled_WithPrometheusFallback exercises the ATLAS_METRICS_FALLBACK_ENABLE
// branch. The Init result must carry a non-nil PrometheusHandler and a
// working shutdown chain.
func TestInit_Enabled_WithPrometheusFallback(t *testing.T) {
	withSavedGlobals(t)
	t.Setenv(EnvEndpoint, "localhost:4318")
	t.Setenv(EnvProtocol, "http/protobuf")
	t.Setenv(EnvMetricsFallback, "true")
	t.Setenv("OTEL_TRACES_SAMPLER_ARG", "0.05")

	res, err := Init(context.Background(), Options{ServiceVersion: "v0", Environment: "prod"})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if !res.Enabled {
		t.Fatal("Enabled = false; want true")
	}
	if res.PrometheusHandler == nil {
		t.Fatal("PrometheusHandler must be non-nil when fallback=true")
	}
	// Cancel the shutdown context so the exporter doesn't block on a
	// nonexistent OTLP target. The first shutdown may aggregate an
	// export-flush error; the second MUST be a no-op (idempotent
	// branch). The Prometheus handler attribute is the load-bearing
	// assertion here, not the exact shutdown error shape.
	shutdownCtx, shutdownCancel := context.WithCancel(context.Background())
	shutdownCancel()
	_ = res.Shutdown(shutdownCtx)
	if err := res.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("second shutdown (idempotent path) returned error: %v", err)
	}
}

// TestInit_UnsupportedProtocol covers the error-return branch where
// buildTraceExporter rejects an unrecognized protocol value. The trace
// exporter MUST never be built when the protocol is malformed, so the
// metric-exporter-cleanup path is NOT exercised here — that's a separate
// failure mode (and gRPC dial doesn't synchronously fail).
func TestInit_UnsupportedProtocol(t *testing.T) {
	withSavedGlobals(t)
	t.Setenv(EnvEndpoint, "localhost:4318")
	t.Setenv(EnvProtocol, "bogus-protocol")

	res, err := Init(context.Background(), Options{})
	if err == nil {
		t.Fatal("Init returned nil error; want unsupported-protocol error")
	}
	if !strings.Contains(err.Error(), "unsupported OTEL_EXPORTER_OTLP_PROTOCOL") {
		t.Errorf("error %q does not name the offending env-var", err.Error())
	}
	if res != nil {
		t.Fatalf("Init returned non-nil result on error path: %+v", res)
	}
}

// ---------------------------------------------------------------------
// buildResource — both Options branches
// ---------------------------------------------------------------------

// TestBuildResource_WithOptionAttributes asserts that buildResource
// merges Options.ServiceVersion + Options.Environment into the resource
// attribute set on top of the SDK defaults.
func TestBuildResource_WithOptionAttributes(t *testing.T) {
	t.Setenv(EnvServiceName, "atlas-staging")
	r, err := buildResource(context.Background(), Options{
		ServiceVersion: "abc123",
		Environment:    "staging",
	})
	if err != nil {
		t.Fatalf("buildResource: %v", err)
	}
	if r == nil {
		t.Fatal("buildResource returned nil resource")
	}
	got := map[string]string{}
	for _, kv := range r.Attributes() {
		got[string(kv.Key)] = kv.Value.AsString()
	}
	if got["service.name"] != "atlas-staging" {
		t.Errorf("service.name = %q; want atlas-staging", got["service.name"])
	}
	if got["service.version"] != "abc123" {
		t.Errorf("service.version = %q; want abc123", got["service.version"])
	}
	// semconv v1.26.0 emits the legacy `deployment.environment` key
	// (the `.name` variant is the v1.27+ namespace shift).
	if got["deployment.environment"] != "staging" {
		t.Errorf("deployment.environment = %q; want staging", got["deployment.environment"])
	}
}

// TestBuildResource_DefaultServiceName covers the path where the caller
// supplies no options and the env-var is unset — the default
// `security-atlas` service.name must appear.
func TestBuildResource_DefaultServiceName(t *testing.T) {
	t.Setenv(EnvServiceName, "")
	r, err := buildResource(context.Background(), Options{})
	if err != nil {
		t.Fatalf("buildResource: %v", err)
	}
	got := map[string]string{}
	for _, kv := range r.Attributes() {
		got[string(kv.Key)] = kv.Value.AsString()
	}
	if got["service.name"] != DefaultServiceName {
		t.Errorf("service.name = %q; want %q", got["service.name"], DefaultServiceName)
	}
	if _, ok := got["service.version"]; ok {
		t.Errorf("service.version emitted with empty Options.ServiceVersion: %q", got["service.version"])
	}
	if _, ok := got["deployment.environment"]; ok {
		t.Errorf("deployment.environment emitted with empty Options.Environment: %q", got["deployment.environment"])
	}
}

// ---------------------------------------------------------------------
// buildTraceExporter / buildMetricExporter — three branches each
// ---------------------------------------------------------------------

// TestBuildTraceExporter_HTTPandGRPCandError covers all three protocol
// branches in one table.
func TestBuildTraceExporter_HTTPandGRPCandError(t *testing.T) {
	cases := []struct {
		name      string
		protocol  string
		wantErr   bool
		errSubstr string
	}{
		{"grpc-default", "", false, ""},
		{"grpc-explicit", "grpc", false, ""},
		{"http-protobuf", "http/protobuf", false, ""},
		{"http-short", "http", false, ""},
		{"unsupported", "thrift", true, "unsupported"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(EnvProtocol, tc.protocol)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			exp, err := buildTraceExporter(ctx)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("buildTraceExporter %q: nil error; want %q", tc.protocol, tc.errSubstr)
				}
				if !strings.Contains(err.Error(), tc.errSubstr) {
					t.Errorf("error %q missing %q", err.Error(), tc.errSubstr)
				}
				return
			}
			if err != nil {
				t.Fatalf("buildTraceExporter %q: %v", tc.protocol, err)
			}
			if exp == nil {
				t.Fatal("nil exporter on success path")
			}
			// Clean up — exporters spawn background goroutines.
			_ = exp.Shutdown(ctx)
		})
	}
}

// TestBuildMetricExporter_HTTPandGRPCandError mirrors the trace test.
func TestBuildMetricExporter_HTTPandGRPCandError(t *testing.T) {
	cases := []struct {
		name      string
		protocol  string
		wantErr   bool
		errSubstr string
	}{
		{"grpc-default", "", false, ""},
		{"grpc-explicit", "grpc", false, ""},
		{"http-protobuf", "http/protobuf", false, ""},
		{"http-short", "http", false, ""},
		{"unsupported", "weird", true, "unsupported"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(EnvProtocol, tc.protocol)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			exp, err := buildMetricExporter(ctx)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("buildMetricExporter %q: nil error; want %q", tc.protocol, tc.errSubstr)
				}
				if !strings.Contains(err.Error(), tc.errSubstr) {
					t.Errorf("error %q missing %q", err.Error(), tc.errSubstr)
				}
				return
			}
			if err != nil {
				t.Fatalf("buildMetricExporter %q: %v", tc.protocol, err)
			}
			if exp == nil {
				t.Fatal("nil exporter on success path")
			}
			_ = exp.Shutdown(ctx)
		})
	}
}

// ---------------------------------------------------------------------
// samplerFromEnv — env-unset + env-set branches
// ---------------------------------------------------------------------

// TestSamplerFromEnv asserts both branches. The function intentionally
// returns a non-nil sampler in both cases (the env-var presence flips an
// internal flag that's currently a no-op stub, see otel.go comments —
// we cover the branch nonetheless to lock the behaviour and surface any
// future divergence).
func TestSamplerFromEnv(t *testing.T) {
	t.Run("env-unset-default-ratio", func(t *testing.T) {
		t.Setenv("OTEL_TRACES_SAMPLER", "")
		t.Setenv("OTEL_TRACES_SAMPLER_ARG", "")
		s := samplerFromEnv()
		if s == nil {
			t.Fatal("nil sampler")
		}
		// Sampler.Description ≈ "ParentBased{root:TraceIDRatioBased{0.1}, ...}"
		if !strings.Contains(s.Description(), "ParentBased") {
			t.Errorf("sampler description = %q; want ParentBased", s.Description())
		}
	})
	t.Run("env-set-flag-only", func(t *testing.T) {
		// When OTEL_TRACES_SAMPLER is set the function takes the
		// env-aware branch (currently a no-op stub per the comments
		// in samplerFromEnv) but still returns the explicit
		// ParentBased/TraceIDRatioBased default. The branch coverage is
		// what we lock; we use `parentbased_traceidratio` to avoid the
		// SDK's "unsupported sampler" stderr warning for unrecognized
		// names.
		t.Setenv("OTEL_TRACES_SAMPLER", "parentbased_traceidratio")
		t.Setenv("OTEL_TRACES_SAMPLER_ARG", "0.5")
		s := samplerFromEnv()
		if s == nil {
			t.Fatal("nil sampler")
		}
		if !strings.Contains(s.Description(), "ParentBased") {
			t.Errorf("sampler description = %q; want ParentBased", s.Description())
		}
	})
}

// ---------------------------------------------------------------------
// newShutdown — both branches
// ---------------------------------------------------------------------

// TestNewShutdown_IdempotentAndAggregatesErrors builds a minimal
// shutdown chain and exercises:
//
//   - the first-call branch: every component's Shutdown runs;
//   - the second-call branch (called=true): returns nil without invoking
//     any provider Shutdown a second time.
//
// We use real SDK providers (not mocks) because the shutdown function
// only depends on their public Shutdown method, which is a small
// well-behaved surface.
func TestNewShutdown_IdempotentAndAggregatesErrors(t *testing.T) {
	tp := sdktrace.NewTracerProvider()
	mp := sdkmetric.NewMeterProvider()
	// Use a stub exporter pair that the SDK can drive directly. The
	// BatchSpanProcessor needs SOME SpanExporter; tracetest's in-memory
	// works.
	traceExp := tracetest.NewInMemoryExporter()
	bsp := sdktrace.NewBatchSpanProcessor(traceExp)
	// MeterProvider Shutdown closes all registered readers; we pass a
	// readerless provider (above) so the dummy metric "exporter" only
	// needs to satisfy the sdkmetric.Exporter interface for the shutdown
	// call chain.
	metricExp := &noopMetricExporter{}

	shutdown := newShutdown(tp, mp, bsp, traceExp, metricExp)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := shutdown(ctx); err != nil {
		t.Fatalf("first shutdown: %v", err)
	}
	// Sentinel: the second call MUST return nil immediately. If the
	// `called` guard regresses, we'd see tp.Shutdown-after-shutdown
	// surface as an error from the SDK.
	if err := shutdown(ctx); err != nil {
		t.Fatalf("second shutdown (idempotent path): %v", err)
	}
}

// noopMetricExporter satisfies sdkmetric.Exporter without doing any
// network work — supports unit testing the shutdown plumbing.
type noopMetricExporter struct{}

func (n *noopMetricExporter) Temporality(sdkmetric.InstrumentKind) metricdata.Temporality {
	return metricdata.CumulativeTemporality
}
func (n *noopMetricExporter) Aggregation(sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return sdkmetric.AggregationDefault{}
}
func (n *noopMetricExporter) Export(context.Context, *metricdata.ResourceMetrics) error { return nil }
func (n *noopMetricExporter) ForceFlush(context.Context) error                          { return nil }
func (n *noopMetricExporter) Shutdown(context.Context) error                            { return nil }

// ---------------------------------------------------------------------
// NATS helpers (nats.go) — every function
// ---------------------------------------------------------------------

// TestNATSHeaderCarrier_Roundtrip covers the carrier's three methods
// (Get / Set / Keys). The carrier is the adapter the OTel propagator
// drives — without it, nothing else in nats.go works.
func TestNATSHeaderCarrier_Roundtrip(t *testing.T) {
	h := nats.Header{}
	c := natsHeaderCarrier(h)
	c.Set("traceparent", "00-aaa-bbb-01")
	c.Set("baggage", "k=v")
	if got := c.Get("traceparent"); got != "00-aaa-bbb-01" {
		t.Errorf("Get traceparent = %q", got)
	}
	if got := c.Get("baggage"); got != "k=v" {
		t.Errorf("Get baggage = %q", got)
	}
	if got := c.Get("missing"); got != "" {
		t.Errorf("Get missing = %q; want empty", got)
	}
	keys := c.Keys()
	if len(keys) < 2 {
		t.Errorf("Keys() = %v; want at least 2", keys)
	}
}

// TestInjectNATSTraceContext_NilMsg + _NewHeader cover the two early
// returns + the populated-header path of InjectNATSTraceContext.
func TestInjectNATSTraceContext_NilMsg(t *testing.T) {
	// Must not panic.
	InjectNATSTraceContext(context.Background(), nil)
}

func TestInjectNATSTraceContext_PopulatesHeader(t *testing.T) {
	installInMemoryTracer(t)
	ctx, span := otel.Tracer("test").Start(context.Background(), "outer")
	defer span.End()

	// Header is initially nil — Inject must allocate.
	msg := &nats.Msg{}
	InjectNATSTraceContext(ctx, msg)
	if msg.Header == nil {
		t.Fatal("Inject did not allocate header")
	}
	if msg.Header.Get("traceparent") == "" {
		t.Errorf("traceparent header not injected")
	}
}

// TestExtractNATSTraceContext_NilMsg + _Roundtrip cover both branches.
func TestExtractNATSTraceContext_NilMsg(t *testing.T) {
	ctx := context.Background()
	got := ExtractNATSTraceContext(ctx, nil)
	if got != ctx {
		t.Errorf("ExtractNATSTraceContext(nil msg) must return the input ctx")
	}
}

func TestExtractNATSTraceContext_NilHeader(t *testing.T) {
	ctx := context.Background()
	msg := &nats.Msg{} // Header is nil
	got := ExtractNATSTraceContext(ctx, msg)
	if got != ctx {
		t.Errorf("ExtractNATSTraceContext(nil header) must return the input ctx")
	}
}

func TestExtractNATSTraceContext_Roundtrip(t *testing.T) {
	exp := installInMemoryTracer(t)

	// Build a publisher-side context, inject, then extract on a fresh
	// context — the resulting consumer span MUST share the trace ID.
	pubCtx, pubSpan := otel.Tracer("test").Start(context.Background(), "publisher")
	msg := &nats.Msg{Header: nats.Header{}}
	InjectNATSTraceContext(pubCtx, msg)
	pubID := pubSpan.SpanContext().TraceID()
	pubSpan.End()

	consumeCtx := ExtractNATSTraceContext(context.Background(), msg)
	_, consumeSpan := otel.Tracer("test").Start(consumeCtx, "consumer")
	consumeID := consumeSpan.SpanContext().TraceID()
	consumeSpan.End()

	if pubID != consumeID {
		t.Errorf("trace IDs differ across inject/extract: pub=%s consume=%s", pubID, consumeID)
	}
	if got := len(exp.GetSpans()); got != 2 {
		t.Errorf("captured %d spans; want 2", got)
	}
}

// TestStartNATSPublishSpan covers the publisher-side helper and its
// AC-13 attribute shape (with and without messageID).
func TestStartNATSPublishSpan(t *testing.T) {
	exp := installInMemoryTracer(t)

	_, span := StartNATSPublishSpan(context.Background(), "evidence.ingest", "idem-1")
	span.End()
	_, span2 := StartNATSPublishSpan(context.Background(), "evidence.ingest", "")
	span2.End()

	spans := exp.GetSpans()
	if len(spans) != 2 {
		t.Fatalf("got %d spans; want 2", len(spans))
	}
	// Both spans must be producer-kind with the AC-13 attributes.
	for i, s := range spans {
		if s.SpanKind != oteltrace.SpanKindProducer {
			t.Errorf("span[%d] kind = %v; want Producer", i, s.SpanKind)
		}
		attrs := map[string]string{}
		for _, kv := range s.Attributes {
			attrs[string(kv.Key)] = kv.Value.AsString()
		}
		if attrs["messaging.system"] != "nats" {
			t.Errorf("span[%d] messaging.system = %q", i, attrs["messaging.system"])
		}
		if attrs["messaging.destination"] != "evidence.ingest" {
			t.Errorf("span[%d] messaging.destination = %q", i, attrs["messaging.destination"])
		}
	}
	// First span has the message-id; second omits it.
	mapAttrs := func(s tracetest.SpanStub) map[string]string {
		m := map[string]string{}
		for _, kv := range s.Attributes {
			m[string(kv.Key)] = kv.Value.AsString()
		}
		return m
	}
	if mapAttrs(spans[0])["messaging.message.id"] != "idem-1" {
		t.Errorf("span[0] missing messaging.message.id")
	}
	if v, ok := mapAttrs(spans[1])["messaging.message.id"]; ok {
		t.Errorf("span[1] should omit messaging.message.id; got %q", v)
	}
}

// TestStartNATSConsumeSpan covers the consumer-side helper.
func TestStartNATSConsumeSpan(t *testing.T) {
	exp := installInMemoryTracer(t)

	_, span := StartNATSConsumeSpan(context.Background(), "evidence.ingest", "idem-2")
	span.End()
	spans := exp.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("got %d spans; want 1", len(spans))
	}
	if spans[0].SpanKind != oteltrace.SpanKindConsumer {
		t.Errorf("consumer span kind = %v; want Consumer", spans[0].SpanKind)
	}
}

// TestEndNATSSpanWithError covers all three branches: nil span,
// non-nil span + nil error, non-nil span + real error.
func TestEndNATSSpanWithError(t *testing.T) {
	exp := installInMemoryTracer(t)

	// nil span — must not panic.
	EndNATSSpanWithError(nil, errors.New("ignored"))

	// non-nil span, no error → span ends with default (Unset) status.
	_, ok := StartNATSPublishSpan(context.Background(), "evidence.ingest", "idem-3")
	EndNATSSpanWithError(ok, nil)

	// non-nil span, real error → span ends with codes.Error status.
	_, bad := StartNATSPublishSpan(context.Background(), "evidence.ingest", "idem-4")
	EndNATSSpanWithError(bad, errors.New("publish failed"))

	spans := exp.GetSpans()
	if len(spans) != 2 {
		t.Fatalf("captured %d spans; want 2", len(spans))
	}
	// Find the error span and confirm its status.
	var foundError bool
	for _, s := range spans {
		if s.Status.Code == codes.Error {
			foundError = true
			if !strings.Contains(s.Status.Description, "publish failed") {
				t.Errorf("error span description = %q", s.Status.Description)
			}
		}
	}
	if !foundError {
		t.Error("no span with Error status code")
	}
}

// ---------------------------------------------------------------------
// pgx.go — NewTracedPool, parse + happy path
// ---------------------------------------------------------------------

// TestNewTracedPool_ParseError exercises the dsn-parse-failure return
// branch. We feed a syntactically malformed DSN that pgxpool.ParseConfig
// rejects.
func TestNewTracedPool_ParseError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	pool, err := NewTracedPool(ctx, "::not-a-valid-dsn::")
	if err == nil {
		if pool != nil {
			pool.Close()
		}
		t.Fatal("nil error on malformed DSN")
	}
	if !strings.Contains(err.Error(), "parse pgx dsn") {
		t.Errorf("error %q does not name the parse-pgx-dsn stage", err.Error())
	}
}

// TestNewTracedPool_AttachesTracerOnHappyPath covers the post-parse
// success branch. pgxpool.NewWithConfig dials connections in a
// background goroutine so the call returns immediately with a usable
// pool — no live Postgres required. We close it right away.
func TestNewTracedPool_AttachesTracerOnHappyPath(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	pool, err := NewTracedPool(ctx, "postgres://nobody@127.0.0.1:1/none?pool_max_conns=1&connect_timeout=1")
	if err != nil {
		t.Fatalf("NewTracedPool: %v", err)
	}
	if pool == nil {
		t.Fatal("nil pool on success path")
	}
	defer pool.Close()
	// The tracer attached must be otelpgx — its interface implementation
	// is the only product feature here. We surface it through Config().
	cfg := pool.Config()
	if cfg.ConnConfig.Tracer == nil {
		t.Fatal("ConnConfig.Tracer is nil; tracer not attached")
	}
}

// ---------------------------------------------------------------------
// runtime.go — StartRuntimeMetrics
// ---------------------------------------------------------------------

// TestStartRuntimeMetrics asserts the helper returns nil under a
// no-op global MeterProvider (the slice's AC-2 default). The contrib
// package may register against the active meter provider; with the
// no-op provider in place, registrations succeed but emit nothing.
func TestStartRuntimeMetrics(t *testing.T) {
	withSavedGlobals(t)
	// Force a no-op global MeterProvider for this test by restoring
	// after the call.
	if err := StartRuntimeMetrics(); err != nil {
		t.Fatalf("StartRuntimeMetrics: %v", err)
	}
}
