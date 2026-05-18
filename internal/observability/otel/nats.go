// NATS trace context propagation (slice 121, Phase 4 — AC-11/12/13).
//
// OTel-Go ships no first-party NATS contrib. We hand-roll the W3C
// `traceparent` header injection (publisher side) and extraction
// (consumer side) over nats.Header. ~30 lines per direction, fully
// covered by the package's integration test.
//
// AC-13: every span carries `messaging.system=nats`,
// `messaging.destination=<subject>` (we use subject as the destination),
// and `messaging.message.id` when the publisher knows it (typically the
// idempotency key in atlas's evidence-ingest path). The message BODY is
// never an attribute (P0-A8).

package otel

import (
	"context"

	"github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// natsTracerName is the trace.Tracer instrumentation name. Stays stable
// so dashboards can filter by it.
const natsTracerName = "github.com/mgoodric/security-atlas/internal/observability/otel/nats"

// natsHeaderCarrier adapts nats.Header to the OTel TextMapCarrier
// interface so the W3C TraceContext propagator can read/write it.
// Slim: only the three methods the propagator uses.
type natsHeaderCarrier nats.Header

func (c natsHeaderCarrier) Get(key string) string { return nats.Header(c).Get(key) }
func (c natsHeaderCarrier) Set(key, value string) { nats.Header(c).Set(key, value) }
func (c natsHeaderCarrier) Keys() []string {
	out := make([]string, 0, len(c))
	for k := range c {
		out = append(out, k)
	}
	return out
}

// InjectNATSTraceContext writes the active span's W3C traceparent +
// baggage into msg.Header so the consumer can continue the trace.
//
// Safe to call when no span is active or when the propagator is a no-op
// (the function is a cheap NO-OP in that case).
func InjectNATSTraceContext(ctx context.Context, msg *nats.Msg) {
	if msg == nil {
		return
	}
	if msg.Header == nil {
		msg.Header = nats.Header{}
	}
	otel.GetTextMapPropagator().Inject(ctx, natsHeaderCarrier(msg.Header))
}

// ExtractNATSTraceContext returns a context whose remote-span context is
// extracted from msg.Header. The consumer then starts a CHILD span under
// this context, producing the linked publisher → consumer trace.
//
// Safe to call when the message carries no traceparent — the returned
// context simply has no remote span and the consumer's span becomes a
// new root.
func ExtractNATSTraceContext(ctx context.Context, msg *nats.Msg) context.Context {
	if msg == nil || msg.Header == nil {
		return ctx
	}
	return otel.GetTextMapPropagator().Extract(ctx, natsHeaderCarrier(msg.Header))
}

// StartNATSPublishSpan opens a producer-side span around a NATS publish.
// Attributes follow OTel messaging semantic conventions (AC-13).
// Caller defers span.End() and calls span.RecordError on error.
//
// messageID is the idempotency key in atlas's evidence-ingest path; it
// MUST NOT be the payload body (P0-A8).
func StartNATSPublishSpan(ctx context.Context, subject, messageID string) (context.Context, trace.Span) {
	tracer := otel.Tracer(natsTracerName)
	ctx, span := tracer.Start(ctx, "nats.publish "+subject,
		trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(natsAttrs(subject, messageID)...),
	)
	return ctx, span
}

// StartNATSConsumeSpan opens a consumer-side span linked to the
// publisher span via the extracted trace context. Caller defers span.End().
func StartNATSConsumeSpan(ctx context.Context, subject, messageID string) (context.Context, trace.Span) {
	tracer := otel.Tracer(natsTracerName)
	ctx, span := tracer.Start(ctx, "nats.consume "+subject,
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(natsAttrs(subject, messageID)...),
	)
	return ctx, span
}

// natsAttrs assembles the AC-13 attribute set. Subject is the
// destination; messageID when present is `messaging.message.id`. The
// MESSAGE BODY is intentionally absent (P0-A8).
func natsAttrs(subject, messageID string) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String("messaging.system", "nats"),
		attribute.String("messaging.destination", subject),
		attribute.String("messaging.destination_kind", "topic"),
	}
	if messageID != "" {
		attrs = append(attrs, attribute.String("messaging.message.id", messageID))
	}
	return attrs
}

// EndNATSSpanWithError stamps an error onto span and marks the span
// status. Convenience for the handler/publisher's defer chain.
func EndNATSSpanWithError(span trace.Span, err error) {
	if span == nil {
		return
	}
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
}
