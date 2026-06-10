package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"

	"github.com/mgoodric/security-atlas/connectors/pagerduty/internal/incidents"
	"github.com/mgoodric/security-atlas/connectors/pagerduty/internal/pdrecord"
)

// MaxBodyBytes bounds a single webhook delivery. PagerDuty v3 incident webhook
// envelopes are small JSON objects (an event wrapper + a summary `data` block);
// 256 KiB is generous. A larger body is rejected with 413 before the receiver
// reads it fully, so a hostile POST cannot exhaust memory (STRIDE DoS).
const MaxBodyBytes int64 = 256 << 10

// incidentEventPrefix is the v3 event-type family the receiver acts on. PagerDuty
// emits incident.triggered / .acknowledged / .resolved / .escalated /
// .reassigned / .annotated / ... — all share this prefix. A non-incident event
// (e.g. service.* or a test ping) is acknowledged with 200 but emits nothing.
const incidentEventPrefix = "incident."

// Verifier authenticates a raw PagerDuty delivery. *HMACVerifier satisfies it;
// the interface keeps the receiver testable with a stub verifier. Verify is
// called BEFORE the body is parsed or any record is built.
type Verifier interface {
	Verify(body []byte, header http.Header) error
}

// Pusher is the narrow Push surface the receiver consumes. The connector's
// sdk.Client satisfies it; the platform-side wire stays push (invariant #3).
type Pusher interface {
	Push(ctx context.Context, record *evidencev1.EvidenceRecord) (*evidencev1.EvidenceReceipt, error)
}

// Config wires a receiver. All fields except Now are required (Now defaults to
// time.Now().UTC()). The observed-at clock is injectable so a webhook-emitted
// record and a pull-emitted record for the same incident within the same UTC hour
// derive the SAME idempotency key via pdrecord.BuildIncident — the cross-profile
// dedup (slice 489 key reuse).
type Config struct {
	// Verifier authenticates each delivery (PagerDuty v3 HMAC).
	Verifier Verifier
	// Pusher emits the built record (push only — invariant #3).
	Pusher Pusher
	// ControlID is attached to the emitted record (e.g. scf:IRO-02, matching the
	// pull profile's incident control so webhook + pull records carry the same
	// control).
	ControlID string
	// ActorID is the connector's source attribution
	// (connector:pagerduty:incidents@<version>).
	ActorID string
	// Service scopes the emitted record (matches the pull profile's --service).
	Service string
	// Environment scopes the emitted record (matches the pull profile's
	// --environment).
	Environment string
	// Now is the observed-at clock; nil falls back to time.Now().UTC(). The
	// builder hour-truncates it so a webhook and a same-hour poll collapse in the
	// ledger.
	Now func() time.Time
}

func (c Config) validate() error {
	switch {
	case c.Verifier == nil:
		return errors.New("pagerduty/webhook: Verifier required")
	case c.Pusher == nil:
		return errors.New("pagerduty/webhook: Pusher required")
	case c.Environment == "":
		return errors.New("pagerduty/webhook: Environment required")
	}
	return nil
}

// Receiver is the source-side PagerDuty webhook receiver. It is an http.Handler
// so it can be mounted on any server; NewServer wires it onto a bounded
// http.Server with the timeouts gosec G112 requires.
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
		cfg.ControlID = "scf:IRO-02"
	}
	if cfg.Service == "" {
		cfg.Service = "pagerduty"
	}
	return &Receiver{cfg: cfg, now: now}, nil
}

// wireDelivery is the SUMMARY-ONLY view of a PagerDuty v3 webhook delivery. The
// `data` block carries the incident's title / description / body free-text in the
// live payload — those fields are intentionally ABSENT from this struct, so
// json.Unmarshal discards them at the decode boundary and the incident free-text
// NEVER enters memory as connector data (P0-489-3 / threat-model I). This mirrors
// the pull client's apiIncidents decode discipline (connectors/pagerduty/internal/
// incidents/client.go) by construction.
type wireDelivery struct {
	Event struct {
		EventType string `json:"event_type"`
		Data      struct {
			ID         string `json:"id"`
			Number     int    `json:"number"`
			Status     string `json:"status"`
			Urgency    string `json:"urgency"`
			CreatedAt  string `json:"created_at"`
			ResolvedAt string `json:"resolved_at"`
			Service    struct {
				ID      string `json:"id"`
				Summary string `json:"summary"`
			} `json:"service"`
		} `json:"data"`
	} `json:"event"`
}

// ServeHTTP implements the receive → verify → parse → build → push pipeline. Only
// POST is accepted. The signature is verified BEFORE the body is parsed; a failed
// verification returns 401 and builds nothing.
func (r *Receiver) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Size-bound the body BEFORE reading it (a hostile POST cannot exhaust
	// memory). MaxBytesReader caps the read and errors past the limit.
	req.Body = http.MaxBytesReader(w, req.Body, MaxBodyBytes)
	body, err := io.ReadAll(req.Body)
	if err != nil {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}

	// Verify BEFORE any work (STRIDE Spoofing, DOMINANT). Reject unsigned /
	// forged / wrong-signature with a bare 401 (no detail leak — the error is not
	// echoed into the response or a record).
	if err := r.cfg.Verifier.Verify(body, req.Header); err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var d wireDelivery
	if err := json.Unmarshal(body, &d); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	if !strings.HasPrefix(d.Event.EventType, incidentEventPrefix) {
		// Authentic delivery, but not an incident-lifecycle event (e.g. a test
		// ping or a service.* event): acknowledge so PagerDuty does not retry,
		// but emit nothing.
		w.WriteHeader(http.StatusOK)
		return
	}

	id := strings.TrimSpace(d.Event.Data.ID)
	if id == "" {
		// Authentic incident event with no incident id; acknowledge but emit
		// nothing (nothing to attribute / dedup against).
		w.WriteHeader(http.StatusOK)
		return
	}

	if err := r.emit(req.Context(), d); err != nil {
		// id and the error string (which can embed the user-provided incident id
		// via the build/push wraps) are %q-escaped so a crafted id cannot forge
		// log entries through embedded newlines (CWE-117 log injection / b245
		// CodeQL lesson).
		log.Printf("pagerduty/webhook: emit incident %q failed: %q", id, err)
		http.Error(w, "upstream error", http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// emit normalizes the verified delivery's SUMMARY fields and pushes the same
// pagerduty.incident_summary.v1 record the pull profile emits (via
// pdrecord.BuildIncident, UNCHANGED — so the webhook-emitted record is shape- and
// idempotency-key-identical to the pull-emitted one, and the two collapse to one
// ledger row for the same incident within the same UTC hour). Split out so it is
// unit-testable without an HTTP round trip.
func (r *Receiver) emit(ctx context.Context, d wireDelivery) error {
	in := normalize(d)
	rec, err := pdrecord.BuildIncident(in, r.cfg.ControlID, r.cfg.ActorID, r.cfg.Service, r.cfg.Environment, r.now())
	if err != nil {
		return fmt.Errorf("build incident record %q: %w", in.ID, err)
	}
	pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := r.cfg.Pusher.Push(pctx, rec); err != nil {
		return fmt.Errorf("push incident %q: %w", in.ID, err)
	}
	return nil
}

// normalize maps the verified webhook's SUMMARY fields into the same
// incidents.Incident the pull profile builds from. status / urgency are coerced to
// the schema enums exactly as the pull collector does (incidents.Collect), so the
// webhook and pull records are byte-identical in shape. The incident free-text is
// not present in wireDelivery, so there is no path here that could place it into
// the result.
func normalize(d wireDelivery) incidents.Incident {
	data := d.Event.Data
	return incidents.Incident{
		ID:          strings.TrimSpace(data.ID),
		Number:      data.Number,
		Status:      normalizeStatus(data.Status),
		Urgency:     normalizeUrgency(data.Urgency),
		ServiceID:   strings.TrimSpace(data.Service.ID),
		ServiceName: strings.TrimSpace(data.Service.Summary),
		CreatedAt:   parseTime(data.CreatedAt),
		ResolvedAt:  parseTime(data.ResolvedAt),
	}
}

// normalizeStatus mirrors incidents.normalizeStatus: PagerDuty statuses are
// exactly triggered / acknowledged / resolved; an unknown status coerces to
// "triggered" (the safest still-open reading) so the schema enum holds.
func normalizeStatus(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "acknowledged":
		return "acknowledged"
	case "resolved":
		return "resolved"
	default:
		return "triggered"
	}
}

// normalizeUrgency mirrors incidents.normalizeUrgency (high / low).
func normalizeUrgency(s string) string {
	if strings.EqualFold(strings.TrimSpace(s), "low") {
		return "low"
	}
	return "high"
}

// parseTime parses an RFC3339 timestamp, returning the zero time on empty/bad
// input (e.g. resolved_at is absent for an open incident). Mirrors
// incidents.parseTime.
func parseTime(s string) time.Time {
	if strings.TrimSpace(s) == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

// Server lifecycle ----------------------------------------------------------

// NewServer wraps a Receiver in a bounded http.Server mounted at path. The
// timeouts satisfy gosec G112 (Slowloris) and bound a slow client; the receiver
// is the long-lived process the connector's `run --profile=subscribe` owns.
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
