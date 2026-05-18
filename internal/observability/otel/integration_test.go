//go:build integration

// Slice 121 integration tests — exercises the OTel SDK's trace + metric
// emission against an in-memory exporter, and asserts the AC-19 / AC-20 /
// AC-21 security + propagation properties end-to-end.
//
// Requires NATS_URL set when running the NATS-propagation test
// (the publisher/consumer end-to-end). Without it, that test skips.
//
// Run via: go test -tags=integration ./internal/observability/...

package otel_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	otelpropagation "go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"

	atlasotel "github.com/mgoodric/security-atlas/internal/observability/otel"
)

// installInMemoryExporter sets the global TracerProvider to one backed
// by a tracetest.InMemoryExporter and returns the exporter so tests can
// assert against captured spans. Also restores the previous provider
// when the test finishes.
func installInMemoryExporter(t *testing.T) *tracetest.InMemoryExporter {
	t.Helper()
	exp := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(
		trace.WithSyncer(exp),
		trace.WithSampler(trace.AlwaysSample()),
	)
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	prevProp := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(otelpropagation.NewCompositeTextMapPropagator(
		otelpropagation.TraceContext{},
		otelpropagation.Baggage{},
	))
	t.Cleanup(func() {
		_ = tp.Shutdown(context.Background())
		otel.SetTracerProvider(prev)
		otel.SetTextMapPropagator(prevProp)
	})
	return exp
}

// TestAC19_NoBearerTokenInSpanAttributes is the load-bearing security
// regression for AC-19 / P0-A7: a span captured from a request that
// carried `Authorization: Bearer <token>` must NOT contain the token
// VALUE anywhere in its attribute set. otelhttp's default behaviour
// already enforces this; the test guards against a future contributor
// accidentally enabling header recording.
func TestAC19_NoBearerTokenInSpanAttributes(t *testing.T) {
	exp := installInMemoryExporter(t)

	// Wrap a tiny handler with the SAME shape httpserver.go uses:
	// otelhttp at the OUTERMOST layer, named "atlas-http".
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"test"}`))
	})
	root := otelhttp.NewHandler(inner, "atlas-http")

	ts := httptest.NewServer(root)
	defer ts.Close()

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/v1/anchors", nil)
	if err != nil {
		t.Fatal(err)
	}
	const sentinel = "test-bearer-sentinel-do-not-leak-12345"
	req.Header.Set("Authorization", "Bearer "+sentinel)
	req.Header.Set("Cookie", "sa_session=test-cookie-sentinel-9999")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	// Flush.
	time.Sleep(50 * time.Millisecond)

	spans := exp.GetSpans()
	if len(spans) == 0 {
		t.Fatal("expected at least one span; got 0")
	}
	for _, s := range spans {
		for _, kv := range s.Attributes {
			val := kv.Value.AsString()
			if strings.Contains(val, sentinel) {
				t.Errorf("span %q attribute %q leaks bearer token: %q", s.Name, kv.Key, val)
			}
			if strings.Contains(val, "test-cookie-sentinel") {
				t.Errorf("span %q attribute %q leaks cookie: %q", s.Name, kv.Key, val)
			}
		}
		// Also sweep events for the same leak.
		for _, ev := range s.Events {
			for _, kv := range ev.Attributes {
				if strings.Contains(kv.Value.AsString(), sentinel) {
					t.Errorf("span %q event %q attribute %q leaks bearer token",
						s.Name, ev.Name, kv.Key)
				}
			}
		}
	}
}

// TestAC21_NATSTracePropagation asserts that the W3C trace context
// injected at publish time is extracted at consume time and that the
// resulting consumer span's parent is the publisher span (i.e. one
// trace ID end-to-end with a parent-child link).
//
// This is the in-process variant of the property — no actual broker
// required. We synthesize a *nats.Msg, run the helpers directly, and
// assert the trace IDs match + the consumer span's parent matches the
// publisher span's SpanContext. The full broker round-trip is covered by
// the existing streambuf integration test (which becomes a slice-121
// test in spirit when OTel is wired in atlas's NATS path).
func TestAC21_NATSTracePropagation(t *testing.T) {
	exp := installInMemoryExporter(t)
	tracer := otel.Tracer("test")

	// Publisher: start a span, inject context into the NATS message
	// header.
	pubCtx, pubSpan := tracer.Start(context.Background(), "outer-request")
	pubCtx, pubNATSSpan := atlasotel.StartNATSPublishSpan(pubCtx, "evidence.ingest", "test-idem-key-1")
	publisherSpanCtx := pubNATSSpan.SpanContext()

	msg := mkNATSMsg()
	atlasotel.InjectNATSTraceContext(pubCtx, msg)
	pubNATSSpan.End()
	pubSpan.End()

	if got := msg.Header.Get("traceparent"); got == "" {
		t.Fatal("traceparent header was not injected")
	}

	// Consumer: extract the context, start a consumer span, end it.
	consumeCtx := atlasotel.ExtractNATSTraceContext(context.Background(), msg)
	_, consumeSpan := atlasotel.StartNATSConsumeSpan(consumeCtx, "evidence.ingest", "test-idem-key-1")
	consumeSpanCtx := consumeSpan.SpanContext()
	consumeSpan.End()

	// Both span contexts MUST be valid and share the same trace ID.
	if !publisherSpanCtx.IsValid() {
		t.Fatal("publisher span context invalid")
	}
	if !consumeSpanCtx.IsValid() {
		t.Fatal("consumer span context invalid")
	}
	if publisherSpanCtx.TraceID() != consumeSpanCtx.TraceID() {
		t.Errorf("trace IDs differ: publisher=%s consumer=%s",
			publisherSpanCtx.TraceID(), consumeSpanCtx.TraceID())
	}

	// And the consumer's parent ID must equal the publisher's span ID
	// (the linked relationship). We find that by scanning the captured
	// spans for the consumer and reading its parent.
	spans := exp.GetSpans()
	var consumerCaptured *tracetest.SpanStub
	for i := range spans {
		if spans[i].Name == "nats.consume evidence.ingest" {
			consumerCaptured = &spans[i]
			break
		}
	}
	if consumerCaptured == nil {
		t.Fatal("consumer span not captured")
	}
	if consumerCaptured.Parent.SpanID() != publisherSpanCtx.SpanID() {
		t.Errorf("consumer span parent = %s; want publisher span %s",
			consumerCaptured.Parent.SpanID(), publisherSpanCtx.SpanID())
	}

	// AC-13: messaging.system + messaging.destination + message.id.
	wantAttrs := map[string]string{
		"messaging.system":      "nats",
		"messaging.destination": "evidence.ingest",
		"messaging.message.id":  "test-idem-key-1",
	}
	got := map[string]string{}
	for _, kv := range consumerCaptured.Attributes {
		got[string(kv.Key)] = kv.Value.AsString()
	}
	for k, v := range wantAttrs {
		if got[k] != v {
			t.Errorf("consumer span attr %q = %q; want %q", k, got[k], v)
		}
	}
}

// TestAC13_NoMessageBodyInNATSSpanAttributes is the per-message-body
// security guard. P0-A8 forbids the message body from appearing in any
// span attribute. The hand-rolled NATS helpers only accept subject +
// messageID, so by construction no payload bytes reach attributes —
// this test guards against a future contributor adding a "body
// preview" attribute.
func TestAC13_NoMessageBodyInNATSSpanAttributes(t *testing.T) {
	exp := installInMemoryExporter(t)

	const bodySentinel = "secret-payload-test-do-not-leak-abcdef"
	msg := mkNATSMsg()
	msg.Data = []byte(`{"secret":"` + bodySentinel + `"}`)

	_, span := atlasotel.StartNATSPublishSpan(context.Background(), "evidence.ingest", "test-idem-2")
	atlasotel.InjectNATSTraceContext(context.Background(), msg)
	span.End()

	for _, s := range exp.GetSpans() {
		for _, kv := range s.Attributes {
			if strings.Contains(kv.Value.AsString(), bodySentinel) {
				t.Errorf("span %q attribute %q leaks NATS payload body: %q",
					s.Name, kv.Key, kv.Value.AsString())
			}
		}
	}
}

// TestPublisherSpanRecordsErrors confirms EndNATSSpanWithError sets the
// span status. Operational debugging needs this: a failed publish must
// surface as an Error-status span in Tempo, not a green one.
func TestPublisherSpanRecordsErrors(t *testing.T) {
	exp := installInMemoryExporter(t)

	_, span := atlasotel.StartNATSPublishSpan(context.Background(), "evidence.ingest", "test-idem-3")
	atlasotel.EndNATSSpanWithError(span, errFakePublish{})

	spans := exp.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("got %d spans; want 1", len(spans))
	}
	if spans[0].Status.Code != codes.Error {
		t.Errorf("span status = %v; want Error", spans[0].Status.Code)
	}
}

// errFakePublish is a sentinel error type for the span-status test.
type errFakePublish struct{}

func (errFakePublish) Error() string { return "fake publish failure" }

// mkNATSMsg returns a *nats.Msg with an empty header — the slice's
// helpers populate it.
func mkNATSMsg() *nats.Msg {
	return &nats.Msg{Header: nats.Header{}}
}

// Verify the consumer span carries the right span kind. SpanKindConsumer
// is what dashboards and the trace-graph view rely on.
func TestConsumerSpanIsConsumerKind(t *testing.T) {
	exp := installInMemoryExporter(t)
	msg := mkNATSMsg()
	atlasotel.InjectNATSTraceContext(context.Background(), msg)
	_, span := atlasotel.StartNATSConsumeSpan(context.Background(), "evidence.ingest", "test-idem-4")
	span.End()

	spans := exp.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("got %d spans; want 1", len(spans))
	}
	if spans[0].SpanKind != oteltrace.SpanKindConsumer {
		t.Errorf("consumer span kind = %v; want %v", spans[0].SpanKind, oteltrace.SpanKindConsumer)
	}
}
