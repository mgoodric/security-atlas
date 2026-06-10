// Package webhook is the shared SOURCE-SIDE webhook receiver for the HRIS
// connector family (Rippling + BambooHR, slice 573). It establishes the
// event-driven (`subscribe`) profile: a long-lived HTTP server that runs INSIDE
// the connector process, receives the vendor's termination / status-change
// webhook delivery, VERIFIES the per-vendor signature BEFORE doing any work,
// re-reads the worker's minimal lifecycle fields, builds the SAME
// hris.worker_lifecycle.v1 record (via the shared workerrecord builder,
// unchanged), and emits it through the existing Push API.
//
// Invariant #3 (CLAUDE.md): the platform-side wire surface is ALWAYS push. This
// receiver is part of the CONNECTOR, not the platform — it does not add any
// inbound API to internal/api/. `subscribe` describes how the connector retrieves
// data FROM the source (a webhook the vendor POSTs to this process); the record
// still leaves the connector via Push, exactly as the pull profile does.
//
// Dominant new threat (slice 573): anyone can POST to a webhook receiver. The
// signature is verified BEFORE any record is built or pushed; an unsigned,
// forged, or wrong-signature delivery is rejected with 401 and never produces a
// record. The body is size-limited before it is read, so an oversized delivery
// cannot exhaust memory.
//
// Over-collection guard (P0-491-3, unchanged): the receiver builds the record by
// re-reading the worker's MINIMAL lifecycle fields through the same read-only
// vendor client the pull profile uses — never beyond the allowed field set. The
// webhook payload itself is treated as a TRIGGER (worker id + event); the
// authoritative lifecycle facts come from the bounded re-read. There is no code
// path here that can place excluded PII into a record.
package webhook

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"

	"github.com/mgoodric/security-atlas/connectors/hris/worker"
	"github.com/mgoodric/security-atlas/connectors/hris/workerrecord"
)

// MaxBodyBytes bounds a single webhook delivery. HRIS termination webhooks are
// small JSON envelopes (a worker id + an event type); 64 KiB is generous. A
// larger body is rejected with 413 before the receiver reads it, so a hostile
// POST cannot exhaust memory.
const MaxBodyBytes int64 = 64 << 10

// Verifier authenticates a raw webhook delivery for one vendor. Implementations
// recompute the vendor's documented signature over the raw request body using the
// per-subscription shared secret and compare it (in constant time) against the
// signature the vendor placed in a header. Verify is called BEFORE the body is
// parsed or any record is built.
//
// body is the exact bytes received (already size-bounded). header is the request
// header set carrying the vendor's signature. A non-nil error rejects the
// delivery; the caller never inspects the error contents in the HTTP response
// (it returns a bare 401) so a verification failure cannot leak detail.
type Verifier interface {
	// Verify returns nil iff the delivery is authentic. Vendor identifies the
	// vendor for log/record attribution.
	Verify(body []byte, header http.Header) error
	// Vendor is the source HRIS this verifier guards. Used to attribute the
	// re-read + the emitted record.
	Vendor() worker.HRIS
}

// WorkerFetcher re-reads ONE worker's minimal lifecycle fields from the source,
// keyed by the HRIS-native worker id carried in the (verified) webhook payload.
// It returns the same PII-bounded worker.RawWorker the pull profile decodes, so
// the over-collection guard is identical. A nil RawWorker (ok=false) means the
// source no longer returns the worker (e.g. fully removed); the caller skips the
// push rather than emitting an empty record.
type WorkerFetcher interface {
	FetchWorker(ctx context.Context, workerID string) (raw worker.RawWorker, ok bool, err error)
}

// Pusher is the narrow Push surface the receiver consumes. The connector's
// sdk.Client satisfies it; the platform-side wire stays push (invariant #3).
type Pusher interface {
	Push(ctx context.Context, record *evidencev1.EvidenceRecord) (*evidencev1.EvidenceReceipt, error)
}

// PayloadParser extracts the triggered worker ids from a verified raw delivery.
// Each vendor's webhook envelope differs; the parser is per-vendor. A single
// delivery can carry MORE THAN ONE changed worker (slice 655: BambooHR delivers
// an employees[] array; a bulk status change fans out to N workers), so the
// parser returns a SLICE of worker ids. An empty slice means the delivery carried
// no actionable worker (the receiver acknowledges it with 200 but emits nothing).
//
// Single-worker vendors (Rippling: the envelope carries exactly one affected
// worker) return a one-element slice — the receiver's fan-out loop is a no-op for
// them and their behavior is identical to the pre-fan-out single-worker path.
type PayloadParser interface {
	ParseWorkerIDs(body []byte) (workerIDs []string, err error)
}

// MaxFanOut bounds the number of distinct workers the receiver re-reads + pushes
// for a single webhook delivery. A delivery carrying more changed employees than
// this is processed up to the cap (the first MaxFanOut distinct ids) and the
// remainder is dropped with a logged warning — a sane bound so a hostile or
// runaway delivery cannot trigger an unbounded fan-out of re-reads. 100 is far
// above any realistic single-delivery bulk change.
const MaxFanOut = 100

// Config wires a receiver. All fields are required except Now (defaults to
// time.Now). The observed-at clock is injectable so a webhook-emitted record and
// a pull-emitted record for the same worker within the same UTC hour derive the
// SAME idempotency key (dedup against the pull profile — slice 573).
type Config struct {
	// Vendor is the source HRIS (rippling | bamboohr).
	Vendor worker.HRIS
	// Verifier authenticates each delivery (per-vendor HMAC).
	Verifier Verifier
	// Parser extracts the worker id from a verified delivery (per-vendor).
	Parser PayloadParser
	// Fetcher re-reads the worker's minimal lifecycle fields.
	Fetcher WorkerFetcher
	// Pusher emits the built record (push only — invariant #3).
	Pusher Pusher
	// ControlID is attached to the emitted record (scf:IAC-22 by default).
	ControlID string
	// ActorID is the connector's source attribution.
	ActorID string
	// Environment scopes the emitted record.
	Environment string
	// Now is the observed-at clock; nil falls back to time.Now. The receiver
	// hour-truncates it so a webhook and a same-hour poll collapse in the ledger.
	Now func() time.Time
}

func (c Config) validate() error {
	switch {
	case c.Vendor == "":
		return errors.New("webhook: Vendor required")
	case c.Verifier == nil:
		return errors.New("webhook: Verifier required")
	case c.Parser == nil:
		return errors.New("webhook: Parser required")
	case c.Fetcher == nil:
		return errors.New("webhook: Fetcher required")
	case c.Pusher == nil:
		return errors.New("webhook: Pusher required")
	case c.Environment == "":
		return errors.New("webhook: Environment required")
	}
	return nil
}

// Receiver is the source-side webhook receiver. It is an http.Handler so it can
// be mounted on any server; NewServer wires it onto a bounded http.Server with
// the timeouts gosec G112 requires.
type Receiver struct {
	cfg Config
	now func() time.Time
}

// NewReceiver builds a Receiver from a validated Config.
func NewReceiver(cfg Config) (*Receiver, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	now := cfg.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if cfg.ControlID == "" {
		cfg.ControlID = "scf:IAC-22"
	}
	return &Receiver{cfg: cfg, now: now}, nil
}

// ServeHTTP implements the receive → verify → parse → re-read → build → push
// pipeline. Only POST is accepted. The signature is verified BEFORE the body is
// parsed; a failed verification returns 401 and builds nothing.
func (r *Receiver) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Size-bound the body BEFORE reading it (a hostile POST cannot exhaust
	// memory). MaxBytesReader caps the read and Errors past the limit.
	req.Body = http.MaxBytesReader(w, req.Body, MaxBodyBytes)
	body, err := io.ReadAll(req.Body)
	if err != nil {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}

	// Verify BEFORE any work. Reject unsigned / forged / wrong-signature with a
	// bare 401 (no detail leak).
	if err := r.cfg.Verifier.Verify(body, req.Header); err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	ids, err := r.cfg.Parser.ParseWorkerIDs(body)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	ids = dedupCap(ids)
	if len(ids) == 0 {
		// Authentic delivery, but no actionable worker (e.g. an unrelated event):
		// acknowledge so the vendor does not retry, but emit nothing.
		w.WriteHeader(http.StatusOK)
		return
	}

	// Fan out: one trigger+re-read+push per changed worker (slice 655). Signature
	// verification already happened ONCE above, before this loop — it is NOT
	// re-run per worker. A per-worker re-read or push failure does not abort the
	// others: the successes are emitted and only the failures are signalled back
	// (so the vendor retries the whole delivery; the already-pushed records
	// collapse against the pull/idempotency key on the retry — D3 slice 573).
	if failed := r.processAll(req.Context(), ids); failed > 0 {
		// At least one worker failed mid-fan-out. The vendor SHOULD retry; 502
		// signals "not acknowledged". The successes are already pushed and
		// dedup-collapse on the retry.
		http.Error(w, "upstream error", http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// dedupCap removes blank/duplicate ids (a delivery that repeats an id must not
// double-push) preserving first-seen order, and bounds the result to MaxFanOut so
// a runaway delivery cannot trigger an unbounded fan-out of re-reads. Over-cap ids
// are dropped with a logged warning.
func dedupCap(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		if len(out) == MaxFanOut {
			log.Printf("webhook: delivery carried more than %d distinct workers; dropping the remainder", MaxFanOut)
			break
		}
		out = append(out, id)
	}
	return out
}

// processAll fans the trigger+re-read+push over each worker id and returns the
// number that FAILED (a transient re-read or push error). A worker the source no
// longer returns (ok=false) is not a failure (it emits nothing). A failure of one
// worker never aborts the rest — the loop continues and only the count of
// failures is returned, so the successes are pushed regardless.
func (r *Receiver) processAll(ctx context.Context, ids []string) int {
	failed := 0
	for _, id := range ids {
		if err := r.process(ctx, id); err != nil {
			// Log and continue — do not let one bad worker drop the whole delivery
			// or fail the others. The id is a non-PII HRIS-native key (safe to log).
			log.Printf("webhook: fan-out worker %s failed: %v", id, err)
			failed++
		}
	}
	return failed
}

// process re-reads ONE worker, builds the shared record, and pushes it. Split out
// so it is unit-testable without an HTTP round trip and so the fan-out loop is a
// trivial wrapper over the single-worker path.
func (r *Receiver) process(ctx context.Context, workerID string) error {
	raw, ok, err := r.cfg.Fetcher.FetchWorker(ctx, workerID)
	if err != nil {
		return fmt.Errorf("re-read worker %s: %w", workerID, err)
	}
	if !ok {
		// Source no longer returns the worker; nothing to emit. Not an error.
		return nil
	}

	// Reuse the shared normalizer + builder UNCHANGED so the webhook-emitted
	// record is byte-identical in shape (and PII guard) to the pull-emitted one,
	// and shares the hour-truncated observed-at that makes the idempotency keys
	// collide for dedup against the pull profile.
	wks := worker.Normalize(r.cfg.Vendor, []worker.RawWorker{raw}, r.now)
	if len(wks) == 0 {
		// Normalize dropped it (missing id); nothing to emit.
		return nil
	}
	rec, err := workerrecord.Build(wks[0], r.cfg.ControlID, r.cfg.ActorID, string(r.cfg.Vendor), r.cfg.Environment)
	if err != nil {
		return fmt.Errorf("build record %s: %w", workerID, err)
	}
	pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := r.cfg.Pusher.Push(pctx, rec); err != nil {
		return fmt.Errorf("push worker %s: %w", workerID, err)
	}
	return nil
}

// Server lifecycle ----------------------------------------------------------

// NewServer wraps a Receiver in a bounded http.Server mounted at path. The
// timeouts satisfy gosec G112 (Slowloris) and bound a slow client; the receiver
// is a long-lived process the connector's `run --profile=subscribe` owns.
func NewServer(addr, path string, rec *Receiver) *http.Server {
	mux := http.NewServeMux()
	mux.Handle(path, rec)
	return &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
}

// Serve runs srv until ctx is cancelled, then drains it with a bounded graceful
// shutdown. It blocks; the connector's run loop calls it. A returned
// http.ErrServerClosed (the normal shutdown path) is squashed to nil.
func Serve(ctx context.Context, srv *http.Server) error {
	errc := make(chan error, 1)
	go func() {
		err := srv.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errc <- err
	}()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
		return <-errc
	}
}
