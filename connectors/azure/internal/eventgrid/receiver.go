package eventgrid

import (
	"context"
	"errors"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/mgoodric/security-atlas/connectors/shared/webhookrecv"
)

// DefaultMaxBodyBytes bounds a single Event Grid delivery. Event Grid batches are
// small JSON arrays; 256 KiB is generous. A larger body is rejected 413 BEFORE the
// receiver reads it fully (the shared MaxBytesReader cap), so a hostile POST cannot
// exhaust memory (threat-model D).
const DefaultMaxBodyBytes int64 = 256 << 10

// maxValidationCodeLen bounds the echoed validationCode so a hostile caller cannot
// make the receiver reflect an unbounded body in the handshake response.
const maxValidationCodeLen = 2048

// DoS bounding defaults (D6). A bounded queue + a coalescing window collapse an
// event storm into one re-read per resource per window tick.
const (
	// DefaultQueueDepth bounds the pending-event queue. A full queue drops the
	// newest event (the pull profile is the reconciliation backstop).
	DefaultQueueDepth = 1024
	// DefaultCoalesceWindow is how long the worker batches events for the same
	// resource before re-reading: N events for one resource within the window
	// collapse to ONE re-read.
	DefaultCoalesceWindow = 5 * time.Second
)

// Rereader re-reads the changed resource via the EXISTING read-only Azure reader
// for resourceType, filters to resourceID, builds the matching EXISTING kind, and
// pushes it. It is supplied by the cmd adapter (where the readers + builders live);
// the eventgrid package stays domain-light. It returns the number of records
// pushed (0 when the resource no longer resolves — e.g. a forged/payload-only event
// whose id matches no real resource: no-fabrication) and an error on a read/push
// failure.
//
// The record's data comes ENTIRELY from this re-read, NEVER from the event payload:
// the event gave only resourceType + resourceID (a TRIGGER); the Rereader reads
// exactly what the pull profile reads (the over-collection guard + builder are
// UNCHANGED).
type Rereader func(ctx context.Context, resourceType ResourceType, resourceID string) (pushed int, err error)

// Config wires a Receiver. Verifier and Rereader are required; the rest carry
// sensible defaults.
type Config struct {
	// Verifier authenticates each real delivery BEFORE it is enqueued (verify-
	// FIRST). Required.
	Verifier *DeliveryKeyVerifier
	// Reread re-reads + emits the changed resource (D4). Required.
	Reread Rereader
	// MaxBodyBytes overrides DefaultMaxBodyBytes (0 = default).
	MaxBodyBytes int64
	// QueueDepth overrides DefaultQueueDepth (0 = default).
	QueueDepth int
	// CoalesceWindow overrides DefaultCoalesceWindow (0 = default).
	CoalesceWindow time.Duration
}

func (c Config) validate() error {
	switch {
	case c.Verifier == nil:
		return errors.New("eventgrid: Verifier required")
	case c.Reread == nil:
		return errors.New("eventgrid: Reread required")
	}
	return nil
}

// pendingEvent is one enqueued change event awaiting a coalesced re-read.
type pendingEvent struct {
	resourceType ResourceType
	resourceID   string
}

// Receiver is the source-side Event Grid receiver. It is an http.Handler so it can
// be wrapped by the validation-handshake adapter (which owns the non-record
// SubscriptionValidation path). NewServer wires it onto a bounded http.Server.
//
// On a verified delivery the handler ENQUEUES the change events and acks 200
// immediately (Event Grid expects a fast ack; a slow handler triggers retries,
// amplifying a storm). A background worker drains the queue, coalesces same-
// resource events within the window, and performs one re-read per distinct resource.
type Receiver struct {
	cfg            Config
	maxBodyBytes   int64
	queue          chan pendingEvent
	coalesceWindow time.Duration

	mu      sync.Mutex
	dropped int // events dropped on a full queue (observability)
}

// NewReceiver builds a Receiver from a validated Config.
func NewReceiver(cfg Config) (*Receiver, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	maxBody := cfg.MaxBodyBytes
	if maxBody <= 0 {
		maxBody = DefaultMaxBodyBytes
	}
	depth := cfg.QueueDepth
	if depth <= 0 {
		depth = DefaultQueueDepth
	}
	window := cfg.CoalesceWindow
	if window <= 0 {
		window = DefaultCoalesceWindow
	}
	return &Receiver{
		cfg:            cfg,
		maxBodyBytes:   maxBody,
		queue:          make(chan pendingEvent, depth),
		coalesceWindow: window,
	}, nil
}

// ServeHTTP implements the receive → verify → enqueue pipeline. The vendor-agnostic
// preamble (POST-only → 405, body cap → 413, verify-FIRST → 401) is the shared
// webhookrecv skeleton; the verified body is then handed to enqueue, which parses
// the events, drops unmapped ones honestly, and enqueues in-scope ones. The actual
// re-read/push happens on the background worker (Run).
func (r *Receiver) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	v := r.requestVerifier(req)
	webhookrecv.Handle(w, req, r.maxBodyBytes, v, r.enqueue)
}

// requestVerifier returns the Verifier the skeleton calls for THIS request. For the
// header credential location the configured DeliveryKeyVerifier reads the header set
// directly. For the query location the credential lives in the request URL (not the
// header set), so this binds the request's query value into a one-shot verifier.
func (r *Receiver) requestVerifier(req *http.Request) webhookrecv.Verifier {
	dk := r.cfg.Verifier
	if dk.Location() == CredentialQuery {
		got := req.URL.Query().Get(dk.QueryName())
		return queryVerifier{dk: dk, value: got}
	}
	return dk
}

// queryVerifier binds a request's query-param credential value into the
// shared one-method Verifier so the verify-FIRST skeleton can authenticate a
// query-located delivery key before any record work.
type queryVerifier struct {
	dk    *DeliveryKeyVerifier
	value string
}

func (q queryVerifier) Verify(_ []byte, _ http.Header) error {
	return q.dk.verifyValue(q.value)
}

// enqueue is the Event-Grid-domain step the skeleton invokes on a VERIFIED raw
// body: parse the batch, drop unmapped events honestly (ack 200), and enqueue
// in-scope change events for the worker. A malformed body returns 400. It never
// re-reads inline — the re-read happens on the worker so the handler acks fast.
func (r *Receiver) enqueue(_ *http.Request, body []byte) int {
	events, err := ParseBatch(body)
	if err != nil {
		// Malformed body. %q-escape the err so a crafted string embedded in the
		// parse error cannot forge log entries via newlines (CWE-117 / b245).
		log.Printf("eventgrid: parse delivery failed: %q", err)
		return http.StatusBadRequest
	}
	for _, e := range events {
		if e.IsValidation() {
			// A validation event reaching the record path means the handshake
			// adapter did not intercept it; ack without enqueue (no record).
			continue
		}
		if e.ResourceType == ResourceUnknown {
			// No reader for this resource type: drop honestly, ack so Event Grid
			// does not retry. %q-escape the user-tainted resource id at the sink.
			log.Printf("eventgrid: dropping event for unmapped resource %q", e.ResourceID)
			continue
		}
		r.offer(pendingEvent{resourceType: e.ResourceType, resourceID: e.ResourceID})
	}
	return http.StatusOK
}

// offer enqueues an event non-blocking; a full queue drops the newest event and
// bumps the dropped counter (the pull profile catches anything dropped).
func (r *Receiver) offer(p pendingEvent) {
	select {
	case r.queue <- p:
	default:
		r.mu.Lock()
		r.dropped++
		dropped := r.dropped
		r.mu.Unlock()
		log.Printf("eventgrid: queue full, dropped event for resource %q (total dropped=%d; pull backstop covers it)", p.resourceID, dropped)
	}
}

// Dropped reports how many events were dropped on a full queue (observability /
// test assertion).
func (r *Receiver) Dropped() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.dropped
}

// Run drains the queue until ctx is cancelled, coalescing events for the SAME
// resource within the coalescing window into ONE re-read (D6). It blocks; the
// connector's subscribe loop runs it in a goroutine alongside Serve.
func (r *Receiver) Run(ctx context.Context) {
	for {
		// Block for the first event (or exit on cancel).
		var first pendingEvent
		select {
		case <-ctx.Done():
			return
		case first = <-r.queue:
		}
		// Coalesce: collect every queued event within the window, deduping by
		// resource id (one re-read per distinct resource).
		pending := map[string]pendingEvent{first.resourceID: first}
		r.drainWindow(ctx, pending)
		for _, p := range pending {
			r.reread(ctx, p)
		}
		if ctx.Err() != nil {
			return
		}
	}
}

// drainWindow collects every queued event arriving within the coalescing window
// into pending, deduping by resource id. A timer bounds the window; ctx cancel
// stops early.
func (r *Receiver) drainWindow(ctx context.Context, pending map[string]pendingEvent) {
	timer := time.NewTimer(r.coalesceWindow)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			return
		case p := <-r.queue:
			pending[p.resourceID] = p
		}
	}
}

// reread invokes the adapter-supplied Rereader for one coalesced resource. A
// re-read/push error is logged (%q-escaped) and swallowed — the pull profile is the
// backstop; a single failed re-read must not crash the long-lived receiver.
func (r *Receiver) reread(ctx context.Context, p pendingEvent) {
	pushed, err := r.cfg.Reread(ctx, p.resourceType, p.resourceID)
	if err != nil {
		log.Printf("eventgrid: re-read %q resource %q failed: %q", string(p.resourceType), p.resourceID, err)
		return
	}
	if pushed == 0 {
		// The event's resource id resolved to no real resource (a forged /
		// payload-only event, or a deleted resource): emit nothing (no-fabrication).
		log.Printf("eventgrid: re-read %q resource %q produced no record (resource not found; no fabrication)", string(p.resourceType), p.resourceID)
	}
}

// Server lifecycle ----------------------------------------------------------

// NewServer wraps an http.Handler in a bounded http.Server mounted at path via the
// shared webhookrecv constructor. handler is typically the validation-handshake
// adapter wrapping a Receiver. The timeouts satisfy gosec G112 (Slowloris).
func NewServer(addr, path string, handler http.Handler) *http.Server {
	return webhookrecv.NewServer(addr, path, handler)
}

// Serve runs srv until ctx is cancelled, then drains it with a bounded graceful
// shutdown via the shared webhookrecv lifecycle. It blocks; the connector's run
// loop calls it.
func Serve(ctx context.Context, srv *http.Server) error {
	return webhookrecv.Serve(ctx, srv)
}
