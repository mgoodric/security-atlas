// Package mdmwebhook is the SOURCE-side webhook-receiver layer shared by the two
// MDM connectors (Jamf + Intune, slice 557). It is a THIN adapter onto
// connectors/shared/webhookrecv (slice 656): it owns nothing of the
// vendor-agnostic machinery (the bounded gosec-G112 http.Server, the graceful
// Serve, the MaxBytesReader body cap, the verify-FIRST → 401 skeleton) — that
// lives in webhookrecv. This package owns only the MDM-domain glue both vendors
// share:
//
//   - parse a verified change-event body into the SAME PII-bounded
//     devposture.RawDevice the pull profile produces (the per-vendor parser
//     supplies the mapping),
//   - normalize + build the SAME endpoint.device_posture.v1 record via
//     devrecord.Build (UNCHANGED — so a webhook-emitted record and a pull-emitted
//     record for the same device collapse to ONE ledger row via the shared idem
//     key), and
//   - push it (push only — invariant #3).
//
// Invariant #3 (CLAUDE.md): the platform-side wire surface is ALWAYS push. This
// package is part of a CONNECTOR, not the platform — it adds NO inbound API to
// internal/api/. `subscribe` is a profiles_supported value describing how the
// connector retrieves data FROM the MDM (event-driven webhook receipt); the
// connector still re-emits via Push.
//
// Over-collection guard (P0-490-3 / threat-model I, REUSED UNCHANGED): the parser
// maps into devposture.RawDevice, whose type system physically cannot hold device
// geolocation, an installed-app inventory, device contents, or owner personal
// contact detail. A webhook payload carrying those fields has nowhere to put them;
// json.Unmarshal discards unmodelled keys at the decode boundary.
package mdmwebhook

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"

	"github.com/mgoodric/security-atlas/connectors/mdm/devposture"
	"github.com/mgoodric/security-atlas/connectors/mdm/devrecord"
	"github.com/mgoodric/security-atlas/connectors/shared/webhookrecv"
)

// DefaultMaxBodyBytes bounds a single change-event delivery. Jamf computer
// webhook envelopes and Microsoft Graph change-notification batches are small
// JSON objects; 256 KiB is generous. A larger body is rejected with 413 BEFORE
// the receiver reads it fully (the shared MaxBytesReader cap), so a hostile POST
// cannot exhaust memory (STRIDE DoS).
const DefaultMaxBodyBytes int64 = 256 << 10

// Pusher is the narrow Push surface the receiver consumes. The connector's
// sdk.Client satisfies it; the platform-side wire stays push (invariant #3).
type Pusher interface {
	Push(ctx context.Context, record *evidencev1.EvidenceRecord) (*evidencev1.EvidenceReceipt, error)
}

// PayloadParser turns a verified change-event body into the PII-bounded
// devposture.RawDevices the delivery reports. Each vendor supplies its own
// (Jamf computer-webhook event vs Graph change-notification). The parser maps
// ONLY the posture-summary fields the pull profile reads — it has nowhere to put
// over-collected fields because devposture.RawDevice has no such field.
//
// A parser returns:
//   - the (possibly empty) slice of devices to emit, and
//   - an error ONLY for a malformed body (→ 400). An authentic-but-empty delivery
//     (e.g. a Graph keep-alive, or an event the connector does not act on) returns
//     a nil/empty slice and a nil error → 200 ack, emit nothing.
type PayloadParser func(body []byte) ([]devposture.RawDevice, error)

// Config wires a Receiver. Verifier, Pusher, Parser and Environment are required;
// the rest carry sensible defaults.
type Config struct {
	// SourceMDM stamps the emitted record's source_mdm (jamf / intune). Required.
	SourceMDM devposture.MDM
	// Verifier authenticates each delivery BEFORE any record is built. Required.
	// Each vendor supplies its own (Jamf shared-secret header; Intune clientState).
	Verifier webhookrecv.Verifier
	// Parser maps a verified body to devices. Required.
	Parser PayloadParser
	// Pusher emits the built record (push only — invariant #3). Required.
	Pusher Pusher
	// ControlID is attached to each emitted record (matches the pull profile's
	// --device-control, default scf:END-04).
	ControlID string
	// ActorID is the connector's source attribution
	// (connector:<vendor>:devices@<version>). Required.
	ActorID string
	// Service scopes the emitted record (matches the pull profile's service).
	Service string
	// Environment scopes the emitted record (matches the pull profile's
	// --environment). Required.
	Environment string
	// MaxBodyBytes overrides DefaultMaxBodyBytes (0 = default).
	MaxBodyBytes int64
	// Now is the observed-at clock; nil falls back to time.Now().UTC(). The
	// builder hour-truncates it so a webhook and a same-hour poll collapse in the
	// ledger (cross-profile dedup).
	Now func() time.Time
}

func (c Config) validate() error {
	switch {
	case c.SourceMDM == "":
		return errors.New("mdmwebhook: SourceMDM required")
	case c.Verifier == nil:
		return errors.New("mdmwebhook: Verifier required")
	case c.Parser == nil:
		return errors.New("mdmwebhook: Parser required")
	case c.Pusher == nil:
		return errors.New("mdmwebhook: Pusher required")
	case c.Environment == "":
		return errors.New("mdmwebhook: Environment required")
	}
	return nil
}

// Receiver is the source-side MDM webhook receiver. It is an http.Handler so it
// can be mounted on any server (or wrapped by a vendor adapter that owns a
// non-record path, e.g. the Intune validationToken handshake). NewServer wires it
// onto a bounded http.Server with the gosec-G112 timeouts.
type Receiver struct {
	cfg          Config
	now          func() time.Time
	maxBodyBytes int64
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
		cfg.ControlID = "scf:END-04"
	}
	if cfg.Service == "" {
		cfg.Service = string(cfg.SourceMDM)
	}
	maxBody := cfg.MaxBodyBytes
	if maxBody <= 0 {
		maxBody = DefaultMaxBodyBytes
	}
	return &Receiver{cfg: cfg, now: now, maxBodyBytes: maxBody}, nil
}

// ServeHTTP implements the receive → verify → parse → build → push pipeline. The
// vendor-agnostic preamble (POST-only → 405, body cap → 413, verify-FIRST → 401)
// is the shared webhookrecv skeleton (slice 656); the verified body is then handed
// to buildAndPush, the MDM-domain adapter.
func (r *Receiver) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	webhookrecv.Handle(w, req, r.maxBodyBytes, r.cfg.Verifier, r.buildAndPush)
}

// ServeHTTPWithValidation is ServeHTTP with a pre-verify validation-handshake hook
// (slice 657). A vendor whose subscription is fronted by an UNSIGNED validation
// request (the Intune Graph validationToken handshake) passes a
// webhookrecv.ValidationHook here; the shared skeleton answers the handshake (echo,
// 200, no record) BEFORE the verify-first delivery path. A nil hook makes this
// identical to ServeHTTP. The verify-first invariant is unchanged for every real
// delivery: a non-handshake delivery still reaches the clientState Verifier before
// any record.
func (r *Receiver) ServeHTTPWithValidation(w http.ResponseWriter, req *http.Request, hook webhookrecv.ValidationHook) {
	webhookrecv.HandleWithValidation(w, req, r.maxBodyBytes, hook, r.cfg.Verifier, r.buildAndPush)
}

// buildAndPush is the MDM-domain adapter the skeleton invokes on a verified raw
// body: parse → normalize → build the SAME endpoint.device_posture.v1 record the
// pull profile emits → push each. It returns the HTTP status the skeleton writes.
// Signature/credential verification has already happened in the skeleton, before
// this runs (verify-FIRST). An authentic-but-empty delivery acks 200 and emits
// nothing.
func (r *Receiver) buildAndPush(req *http.Request, body []byte) int {
	raw, err := r.cfg.Parser(body)
	if err != nil {
		// Malformed body. The error is %q-escaped so a crafted device id embedded
		// in the parse error cannot forge log entries via newlines (CWE-117 /
		// b245 CodeQL lesson).
		log.Printf("mdmwebhook: parse %q delivery failed: %q", string(r.cfg.SourceMDM), err)
		return http.StatusBadRequest
	}
	devs := devposture.Normalize(r.cfg.SourceMDM, raw, r.now)
	if len(devs) == 0 {
		// Authentic delivery the connector does not act on (keep-alive, an event
		// carrying no posture-bearing device): ack so the source does not retry,
		// emit nothing.
		return http.StatusOK
	}
	if err := r.emit(req.Context(), devs); err != nil {
		return http.StatusBadGateway
	}
	return http.StatusOK
}

// emit builds + pushes one record per device via devrecord.Build (UNCHANGED).
// Split out so it is unit-testable without an HTTP round trip. Any build/push
// failure stops and returns the error (the source retries the whole delivery;
// already-pushed devices dedup-collapse via the hour-truncated idem key).
func (r *Receiver) emit(ctx context.Context, devs []devposture.Device) error {
	for _, dev := range devs {
		rec, err := devrecord.Build(dev, r.cfg.ControlID, r.cfg.ActorID, r.cfg.Service, r.cfg.Environment)
		if err != nil {
			// dev.DeviceID is user-tainted (it originates in the verified webhook
			// body); %q-escape it AND the err at the sink (CWE-117).
			log.Printf("mdmwebhook: build %q device %q record failed: %q", string(r.cfg.SourceMDM), dev.DeviceID, err)
			return fmt.Errorf("build device record %q: %w", dev.DeviceID, err)
		}
		pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, err = r.cfg.Pusher.Push(pctx, rec)
		cancel()
		if err != nil {
			log.Printf("mdmwebhook: push %q device %q failed: %q", string(r.cfg.SourceMDM), dev.DeviceID, err)
			return fmt.Errorf("push device %q: %w", dev.DeviceID, err)
		}
	}
	return nil
}

// Server lifecycle ----------------------------------------------------------

// NewServer wraps an http.Handler in a bounded http.Server mounted at path via
// the shared webhookrecv constructor. handler is typically a Receiver, or a
// vendor adapter wrapping one (Intune's validationToken handshake). The timeouts
// satisfy gosec G112 (Slowloris) and bound a slow client.
func NewServer(addr, path string, handler http.Handler) *http.Server {
	return webhookrecv.NewServer(addr, path, handler)
}

// Serve runs srv until ctx is cancelled, then drains it with a bounded graceful
// shutdown via the shared webhookrecv lifecycle. It blocks; the connector's run
// loop calls it.
func Serve(ctx context.Context, srv *http.Server) error {
	return webhookrecv.Serve(ctx, srv)
}
