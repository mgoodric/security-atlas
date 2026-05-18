// Package streambuf is the NATS JetStream evidence buffer (slice 015).
// It sits between the push API and the ingestion-stage Service.Process:
//
//	push HTTP/gRPC handler
//	    │  Publish(record, cred)            <-- AC-2: ack here, not at ledger write
//	    ▼
//	NATS JetStream stream `EVIDENCE_INGEST` (subject `evidence.ingest`)
//	    │
//	    ▼  pull-consumer with manual ack
//	Consumer.Process(record)
//	    │  ingest.Service.Process            <-- existing slice-013 contract preserved
//	    ▼
//	evidence_records ledger
//
// AC mapping:
//
//	AC-1  docker-compose + CI ship a NATS service                    (deploy)
//	AC-2  Publisher returns Receipt at stream-commit time            (this package)
//	AC-3  Consumer reads from stream, calls Service.Process          (this package)
//	AC-4  Replay test asserts exactly-once-on-ledger after restart   (integration test)
//	AC-5  Stream MaxAge = 7 days                                     (Config.MaxAge default)
//	AC-6  Redaction rules applied by Service.Process via slice-014   (separate package)
//
// Constitutional invariant 2 (canvas §4.3): ingestion and evaluation
// remain separated. JetStream is part of the ingestion stage's
// substrate; evaluation never reads from the stream and never writes
// to it.
package streambuf

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/canonjson"
	"github.com/mgoodric/security-atlas/internal/evidence/ingest"
	atlasotel "github.com/mgoodric/security-atlas/internal/observability/otel"
)

// Defaults. Documented values per canvas §9.3 / EVIDENCE_SDK §4.6.
const (
	// DefaultStreamName is the JetStream stream that holds buffered
	// evidence records.
	DefaultStreamName = "EVIDENCE_INGEST"
	// DefaultSubject is the publish subject. The stream binds it.
	DefaultSubject = "evidence.ingest"
	// DefaultConsumerName is the durable consumer the ingestion stage runs.
	DefaultConsumerName = "evidence_ingest_worker"
	// DefaultMaxAge satisfies AC-5: 7-day stream retention.
	DefaultMaxAge = 7 * 24 * time.Hour
	// DefaultPublishTimeout is the synchronous-publish bound.
	DefaultPublishTimeout = 5 * time.Second
	// DefaultAckWait is the consumer ack window. If Process blocks
	// longer than this, JetStream redelivers — at-least-once is
	// preserved.
	DefaultAckWait = 60 * time.Second
)

// Header keys we set on every published message. Used by the consumer
// to enforce auth context without re-parsing the payload.
const (
	HeaderCredentialID     = "X-Atlas-Credential-Id"
	HeaderCredentialTenant = "X-Atlas-Credential-Tenant"
	HeaderCredentialKinds  = "X-Atlas-Credential-Kinds"
	HeaderCredentialScope  = "X-Atlas-Credential-Scope"
	HeaderIdempotencyKey   = "X-Atlas-Idempotency-Key"
	HeaderEvidenceKind     = "X-Atlas-Evidence-Kind"
)

// Config is the streambuf wiring. Zero values are filled with sensible
// defaults; required fields are validated by Open.
type Config struct {
	// URL is the NATS URL, e.g. "nats://localhost:4222". Required.
	URL string
	// StreamName overrides DefaultStreamName.
	StreamName string
	// Subject overrides DefaultSubject.
	Subject string
	// ConsumerName overrides DefaultConsumerName.
	ConsumerName string
	// MaxAge overrides DefaultMaxAge.
	MaxAge time.Duration
	// MaxBytes caps total stream size. 0 = unlimited.
	MaxBytes int64
	// PublishTimeout overrides DefaultPublishTimeout.
	PublishTimeout time.Duration
	// AckWait overrides DefaultAckWait.
	AckWait time.Duration
	// Logger is used for non-payload diagnostics. Required.
	// Anti-criterion P0: callers MUST NOT pass a logger that captures
	// payload bytes. The streambuf code itself logs only metadata
	// (idempotency key, evidence kind, decision, credential id).
	Logger *slog.Logger
}

func (c *Config) applyDefaults() {
	if c.StreamName == "" {
		c.StreamName = DefaultStreamName
	}
	if c.Subject == "" {
		c.Subject = DefaultSubject
	}
	if c.ConsumerName == "" {
		c.ConsumerName = DefaultConsumerName
	}
	if c.MaxAge == 0 {
		c.MaxAge = DefaultMaxAge
	}
	if c.PublishTimeout == 0 {
		c.PublishTimeout = DefaultPublishTimeout
	}
	if c.AckWait == 0 {
		c.AckWait = DefaultAckWait
	}
}

// Conn bundles the NATS connection + JetStream API + the stream binding.
// Open creates or updates the stream to match Config; Close drains and
// disconnects. The cred URL (if any) is redacted out of any logged
// connection string per the slice 015 P0 anti-criterion.
type Conn struct {
	cfg    Config
	nc     *nats.Conn
	js     jetstream.JetStream
	stream jetstream.Stream
}

// Open dials NATS, creates/updates the EVIDENCE_INGEST stream to match
// Config, and returns a connected Conn. The stream is configured with
// Limits retention + file storage + MaxAge = 7 days so the consumer can
// replay records after a restart (AC-4 / AC-5).
//
// The caller-provided logger receives stream lifecycle events but never
// the payload bodies. NATS credentials are stripped from any URL we
// log (P0 anti-criterion).
func Open(ctx context.Context, cfg Config) (*Conn, error) {
	if cfg.URL == "" {
		return nil, errors.New("streambuf: Config.URL is required")
	}
	if cfg.Logger == nil {
		return nil, errors.New("streambuf: Config.Logger is required")
	}
	cfg.applyDefaults()

	nc, err := nats.Connect(cfg.URL,
		nats.Name("security-atlas-evidence-ingest"),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("streambuf: nats.Connect %q: %w", redactURL(cfg.URL), err)
	}

	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("streambuf: jetstream.New: %w", err)
	}

	streamCfg := jetstream.StreamConfig{
		Name:        cfg.StreamName,
		Description: "security-atlas evidence ingest buffer (slice 015)",
		Subjects:    []string{cfg.Subject},
		// AC-5: 7-day retention with Limits policy so the consumer can
		// replay records (WorkQueue policy deletes on ack, defeating
		// replay).
		Retention: jetstream.LimitsPolicy,
		// AC-4: replay capability. File storage survives a process
		// restart.
		Storage:  jetstream.FileStorage,
		MaxAge:   cfg.MaxAge,
		MaxBytes: cfg.MaxBytes,
		// Discard old once the stream hits its caps — the consumer is
		// expected to keep up; old records are eligible for cleanup by
		// MaxAge anyway.
		Discard: jetstream.DiscardOld,
		// AC-4 / dedup: enable JetStream message dedup. The publisher
		// sets `Nats-Msg-Id` to the idempotency key, so a same-key
		// re-publish inside the dedup window is collapsed at the
		// stream level (in addition to the existing dedup at
		// Service.Process via evidence_records.idempotency_key).
		Duplicates: 2 * time.Minute,
	}

	stream, err := js.CreateOrUpdateStream(ctx, streamCfg)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("streambuf: CreateOrUpdateStream %q: %w", cfg.StreamName, err)
	}
	cfg.Logger.Info("streambuf: stream ready",
		slog.String("stream", cfg.StreamName),
		slog.String("subject", cfg.Subject),
		slog.Duration("max_age", cfg.MaxAge),
	)

	return &Conn{cfg: cfg, nc: nc, js: js, stream: stream}, nil
}

// Close drains the NATS connection and releases resources. Safe to call
// multiple times.
func (c *Conn) Close() {
	if c == nil || c.nc == nil {
		return
	}
	_ = c.nc.Drain()
	c.nc = nil
}

// Stream returns the configured JetStream stream. Useful for tests
// asserting AC-5 (stream config) inspection.
func (c *Conn) Stream() jetstream.Stream { return c.stream }

// JS returns the underlying JetStream context.
func (c *Conn) JS() jetstream.JetStream { return c.js }

// Cfg returns the resolved configuration with defaults applied.
func (c *Conn) Cfg() Config { return c.cfg }

// ---- Publisher ----

// Publisher is the slice-015 push-API substrate hook. JetStreamPublisher
// publishes to the stream and returns the publish-time receipt; the
// in-process DirectPublisher (used by unit-only servers) calls
// Service.Process inline.
type Publisher interface {
	Publish(ctx context.Context, rec *evidencev1.EvidenceRecord, cred credstore.Credential) (ingest.Receipt, ingest.Decision, error)
}

// JetStreamPublisher implements Publisher against a JetStream stream.
// AC-2: ack returns immediately after stream commit (not after ledger
// write). The hash is the canonjson sha256 of the record (computed
// before publish). The record_id is a UUID derived deterministically
// from (tenant, idempotency_key, hash) so retries return a stable id
// even before the consumer writes.
type JetStreamPublisher struct {
	conn   *Conn
	logger *slog.Logger
	now    func() time.Time
}

// NewJetStreamPublisher constructs a Publisher backed by `conn`.
func NewJetStreamPublisher(conn *Conn) *JetStreamPublisher {
	return &JetStreamPublisher{
		conn:   conn,
		logger: conn.cfg.Logger,
		now:    func() time.Time { return time.Now().UTC() },
	}
}

// Publish serializes the record and publishes it. Returns
// ingest.DecisionAccepted on stream-commit success. The receipt's
// record_id is a publish-time deterministic UUID; the durable
// evidence_records row is written by the consumer.
func (p *JetStreamPublisher) Publish(ctx context.Context, rec *evidencev1.EvidenceRecord, cred credstore.Credential) (ingest.Receipt, ingest.Decision, error) {
	if rec == nil {
		return ingest.Receipt{}, ingest.DecisionRejectedValidation, errors.New("streambuf: nil record")
	}
	if cred.TenantID == "" {
		return ingest.Receipt{}, ingest.DecisionRejectedUnauthenticated, errors.New("streambuf: credential has no tenant")
	}
	bytes, err := proto.Marshal(rec)
	if err != nil {
		return ingest.Receipt{}, ingest.DecisionRejectedValidation, fmt.Errorf("streambuf: marshal record: %w", err)
	}
	hash, err := canonjson.HashRecord(rec)
	if err != nil {
		return ingest.Receipt{}, ingest.DecisionRejectedInternalError, fmt.Errorf("streambuf: hash record: %w", err)
	}
	// Deterministic publish-time record_id: UUIDv5 over (tenant, idem, hash).
	// Provisional — the consumer may collapse to an existing row via
	// idempotency dedup inside Service.Process, in which case the final
	// receipt to a subsequent pull would carry a different id. The
	// HTTP response surface documents this in the slice-015 changelog
	// entry.
	recordID := uuid.NewSHA1(uuid.MustParse("00000000-0000-0000-0000-000000015000"),
		[]byte(cred.TenantID+"|"+rec.IdempotencyKey+"|"+hash)).String()

	// Slice 121 (AC-11/13): wrap the publish in a producer-side span so
	// the downstream consumer can link its handler span to this trace.
	// `messaging.message.id` carries the idempotency key — never the
	// payload body (P0-A8). pubErr is the carry-out from the publish
	// branch; the deferred End stamps it on the span (success → no
	// error, broker failure → recorded + Error status).
	pubSpanCtx, pubSpan := atlasotel.StartNATSPublishSpan(ctx, p.conn.cfg.Subject, rec.IdempotencyKey)
	var pubErr error
	defer func() { atlasotel.EndNATSSpanWithError(pubSpan, pubErr) }()

	msg := &nats.Msg{
		Subject: p.conn.cfg.Subject,
		Data:    bytes,
		Header:  nats.Header{},
	}
	// Slice 121 (AC-11): inject the W3C traceparent header before the
	// other headers so the consumer's Extract finds it on every message.
	// When OTel is in no-op mode (AC-2) the propagator writes nothing
	// and this is a cheap no-op.
	atlasotel.InjectNATSTraceContext(pubSpanCtx, msg)
	// Nats-Msg-Id enables JetStream's in-stream dedup window — a same-id
	// re-publish inside the window is collapsed by the broker. The
	// stream-level dedup is a defense-in-depth complement to the
	// Service.Process idempotency check at the ledger.
	msg.Header.Set(nats.MsgIdHdr, cred.TenantID+"|"+rec.IdempotencyKey)
	msg.Header.Set(HeaderCredentialID, cred.ID)
	msg.Header.Set(HeaderCredentialTenant, cred.TenantID)
	msg.Header.Set(HeaderIdempotencyKey, rec.IdempotencyKey)
	msg.Header.Set(HeaderEvidenceKind, rec.EvidenceKind)
	if len(cred.Kinds) > 0 {
		msg.Header.Set(HeaderCredentialKinds, strings.Join(cred.Kinds, ","))
	}
	if cred.ScopePredicate != "" {
		msg.Header.Set(HeaderCredentialScope, cred.ScopePredicate)
	}

	pubCtx, cancel := context.WithTimeout(pubSpanCtx, p.conn.cfg.PublishTimeout)
	defer cancel()

	if _, perr := p.conn.js.PublishMsg(pubCtx, msg); perr != nil {
		pubErr = perr
		return ingest.Receipt{}, ingest.DecisionRejectedInternalError, fmt.Errorf("streambuf: publish: %w", perr)
	}

	// AC-2: ack here, BEFORE the ledger write. Anti-criterion P0: we do
	// NOT log the payload bytes. We log only metadata.
	p.logger.Debug("streambuf: published",
		slog.String("kind", rec.EvidenceKind),
		slog.String("idempotency_key", rec.IdempotencyKey),
		slog.String("credential_id", cred.ID),
	)

	return ingest.Receipt{
		RecordID:     recordID,
		Hash:         hash,
		IngestedAt:   p.now(),
		CredentialID: cred.ID,
		Deduplicated: false,
	}, ingest.DecisionAccepted, nil
}

// DirectPublisher implements Publisher by calling Service.Process inline.
// Used when no NATS substrate is wired — unit-only tests, dev mode
// without NATS, etc. Provides backwards compatibility with slice 013's
// existing path.
type DirectPublisher struct {
	svc *ingest.Service
}

// NewDirectPublisher wraps an ingest.Service as a Publisher.
func NewDirectPublisher(svc *ingest.Service) *DirectPublisher {
	return &DirectPublisher{svc: svc}
}

// Publish calls Service.Process. Receipt/decision/error are surfaced
// verbatim — this is exactly the slice-013 path with no buffering.
func (p *DirectPublisher) Publish(ctx context.Context, rec *evidencev1.EvidenceRecord, cred credstore.Credential) (ingest.Receipt, ingest.Decision, error) {
	return p.svc.Process(ctx, rec, cred)
}

// ---- Consumer ----

// Consumer reads from the JetStream stream and runs Service.Process for
// each message. AC-3 / AC-4: the consumer is the ledger writer; it
// preserves at-least-once by Ack-ing only after Process returns success.
//
// Anti-criterion P0: the consumer never logs payload bodies. It logs
// metadata only: subject, idempotency key, evidence kind, decision,
// processing time.
type Consumer struct {
	conn     *Conn
	svc      *ingest.Service
	logger   *slog.Logger
	durable  string
	running  atomic.Bool
	stopOnce sync.Once
	stopCh   chan struct{}
	// processed counts every Ack-ed message; tests assert it.
	processed atomic.Int64
	// reprocessed counts dedup hits inside Process; tests assert it.
	reprocessed atomic.Int64
}

// NewConsumer constructs a Consumer.
func NewConsumer(conn *Conn, svc *ingest.Service) *Consumer {
	return &Consumer{
		conn:    conn,
		svc:     svc,
		logger:  conn.cfg.Logger,
		durable: conn.cfg.ConsumerName,
		stopCh:  make(chan struct{}),
	}
}

// Start runs the consumer until ctx is canceled or Stop is called.
// Blocks on the calling goroutine; tests typically run it in a
// background goroutine.
func (c *Consumer) Start(ctx context.Context) error {
	if !c.running.CompareAndSwap(false, true) {
		return errors.New("streambuf: consumer already running")
	}
	defer c.running.Store(false)

	consumer, err := c.conn.stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Durable:       c.durable,
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       c.conn.cfg.AckWait,
		MaxDeliver:    -1, // unlimited redeliveries; Process is idempotent
		DeliverPolicy: jetstream.DeliverAllPolicy,
		ReplayPolicy:  jetstream.ReplayInstantPolicy,
	})
	if err != nil {
		return fmt.Errorf("streambuf: consumer create: %w", err)
	}

	c.logger.Info("streambuf: consumer started",
		slog.String("durable", c.durable),
		slog.String("subject", c.conn.cfg.Subject),
	)

	msgs, err := consumer.Messages(jetstream.PullMaxMessages(64))
	if err != nil {
		return fmt.Errorf("streambuf: consumer.Messages: %w", err)
	}
	defer msgs.Stop()

	// Run a goroutine that closes msgs on stop signal.
	go func() {
		select {
		case <-ctx.Done():
		case <-c.stopCh:
		}
		msgs.Stop()
	}()

	for {
		msg, err := msgs.Next()
		if err != nil {
			if errors.Is(err, jetstream.ErrMsgIteratorClosed) {
				return nil
			}
			// Transient pull errors: log and continue.
			c.logger.Warn("streambuf: pull error",
				slog.String("err", err.Error()),
			)
			continue
		}
		c.handle(ctx, msg)
	}
}

// Stop signals Start to return.
func (c *Consumer) Stop() {
	c.stopOnce.Do(func() { close(c.stopCh) })
}

// Processed returns the count of messages that landed Ack-ed (success
// path). Tests assert this for AC-3 / AC-4.
func (c *Consumer) Processed() int64 { return c.processed.Load() }

// Reprocessed returns the count of messages that resulted in a
// DecisionDeduplicated outcome at Service.Process (i.e. idempotency hit).
// Tests assert this for AC-4's exactly-once-on-ledger property.
func (c *Consumer) Reprocessed() int64 { return c.reprocessed.Load() }

// handle decodes one message, recovers the credential context, and runs
// Service.Process. Ack-only-on-success preserves at-least-once.
//
// Anti-criterion P0: the only payload-bearing object reached in this
// function is `msg.Data()` and the unmarshaled `rec`. Neither is
// logged. Errors are logged with metadata + the error string only.
func (c *Consumer) handle(ctx context.Context, msg jetstream.Msg) {
	start := time.Now()
	hdr := msg.Headers()
	cred := credstore.Credential{
		ID:             hdr.Get(HeaderCredentialID),
		TenantID:       hdr.Get(HeaderCredentialTenant),
		ScopePredicate: hdr.Get(HeaderCredentialScope),
	}
	if k := hdr.Get(HeaderCredentialKinds); k != "" {
		cred.Kinds = strings.Split(k, ",")
	}

	// Slice 121 (AC-12): extract the W3C traceparent the publisher
	// injected so the consumer-side span links back to the producer
	// span. jetstream.Msg.Headers() is the same nats.Header shape used
	// in publish. Then start the consumer span; ctx flows through
	// Service.Process so the DB span underneath (otelpgx) becomes a
	// grandchild of the producer span — one trace from HTTP push to
	// ledger write.
	extracted := atlasotel.ExtractNATSTraceContext(ctx, &nats.Msg{Header: hdr})
	consumeCtx, consumeSpan := atlasotel.StartNATSConsumeSpan(extracted, c.conn.cfg.Subject, hdr.Get(HeaderIdempotencyKey))
	var consumeErr error
	defer func() { atlasotel.EndNATSSpanWithError(consumeSpan, consumeErr) }()
	ctx = consumeCtx

	var rec evidencev1.EvidenceRecord
	if err := proto.Unmarshal(msg.Data(), &rec); err != nil {
		// Poison message: cannot decode. Term the message so it does
		// not redeliver forever.
		c.logger.Warn("streambuf: unmarshal failed; terming",
			slog.String("err", err.Error()),
			slog.String("idempotency_key", hdr.Get(HeaderIdempotencyKey)),
		)
		consumeErr = err
		_ = msg.Term()
		return
	}

	_, decision, perr := c.svc.Process(ctx, &rec, cred)
	if perr != nil {
		consumeErr = perr
		// On idempotency mismatch or validation error, the message is
		// poison and we Term it (a Nak would just redeliver forever).
		// On internal/transient errors, Nak to redeliver after AckWait.
		if isPoison(decision) {
			c.logger.Warn("streambuf: poison; terming",
				slog.String("decision", decision.String()),
				slog.String("idempotency_key", hdr.Get(HeaderIdempotencyKey)),
				slog.String("kind", hdr.Get(HeaderEvidenceKind)),
				slog.String("err", perr.Error()),
				slog.Duration("elapsed", time.Since(start)),
			)
			_ = msg.Term()
			return
		}
		c.logger.Warn("streambuf: process error; will redeliver",
			slog.String("decision", decision.String()),
			slog.String("idempotency_key", hdr.Get(HeaderIdempotencyKey)),
			slog.String("err", perr.Error()),
		)
		_ = msg.NakWithDelay(2 * time.Second)
		return
	}

	if decision == ingest.DecisionDeduplicated {
		c.reprocessed.Add(1)
	}
	c.processed.Add(1)
	if err := msg.Ack(); err != nil {
		// Failed Ack means the message will redeliver — Process is
		// idempotent so this is safe.
		c.logger.Warn("streambuf: ack failed",
			slog.String("err", err.Error()),
			slog.String("idempotency_key", hdr.Get(HeaderIdempotencyKey)),
		)
		return
	}
	c.logger.Debug("streambuf: processed",
		slog.String("decision", decision.String()),
		slog.String("idempotency_key", hdr.Get(HeaderIdempotencyKey)),
		slog.String("kind", hdr.Get(HeaderEvidenceKind)),
		slog.Duration("elapsed", time.Since(start)),
	)
}

// isPoison returns true when Process's decision indicates a message
// that should not be redelivered (it would fail every time).
func isPoison(d ingest.Decision) bool {
	switch d {
	case ingest.DecisionRejectedValidation,
		ingest.DecisionRejectedUnknownKind,
		ingest.DecisionRejectedIdempotencyMismatch,
		ingest.DecisionRejectedScopeViolation,
		ingest.DecisionRejectedObservedAtSkew,
		ingest.DecisionRejectedOversized,
		ingest.DecisionRejectedUnauthenticated:
		return true
	}
	return false
}

// ---- helpers ----

// redactURL strips the user:pass portion of a NATS URL so we can log
// the host without leaking credentials. Anti-criterion P0.
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "<unparseable>"
	}
	if u.User != nil {
		u.User = url.User("REDACTED")
	}
	return u.String()
}
