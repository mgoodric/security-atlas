// Package githubwebhook implements the HTTP receiver for GitHub
// organization webhooks. Anti-criterion P0: every accepted delivery
// passes HMAC-SHA256 signature verification with constant-time compare;
// every record carries idempotency_key derived from X-GitHub-Delivery.
//
// The package exposes:
//
//   - VerifySignature: pure HMAC verifier. Importable by tests.
//   - Sign: companion HMAC signer. Used only by tests to generate fixtures
//     — the binary never signs outbound webhooks (we only verify
//     incoming ones).
//   - Handler: net/http handler that verifies + transforms +
//     hands off to the supplied Pusher. Constructor enforces a non-empty
//     secret so the binary cannot start without one.
package githubwebhook

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mgoodric/security-atlas/connectors/shared/webhookrecv"
)

const (
	// HeaderSignature is GitHub's HMAC-SHA256 signature header.
	HeaderSignature = "X-Hub-Signature-256"
	// HeaderEvent carries the event type (repository, member, push, ...).
	HeaderEvent = "X-GitHub-Event"
	// HeaderDelivery is GitHub's per-delivery UUID. We use it directly as
	// the evidence record idempotency_key (anti-criterion P0).
	HeaderDelivery = "X-GitHub-Delivery"

	// signaturePrefix is the literal "sha256=" prefix every X-Hub-Signature-256
	// value begins with.
	signaturePrefix = "sha256="
)

// ghHMAC is the shared HMAC config for GitHub's scheme: lowercase hex digest
// behind the "sha256=" prefix in X-Hub-Signature-256, single signature (slice 656
// factored core). GitHub keeps its own VerifySignature wrapper because its
// missing-vs-malformed-vs-bad error taxonomy is asserted by callers and is
// finer-grained than the shared ErrUnsigned/ErrBadSignature pair; the wrapper
// reuses the shared core only for the constant-time digest match.
var ghHMAC = webhookrecv.HMACConfig{
	Header:   HeaderSignature,
	Prefix:   signaturePrefix,
	Encoding: webhookrecv.EncodingHex,
}

// VerifySignature returns nil iff sigHeader is a syntactically-valid
// X-Hub-Signature-256 value AND its HMAC-SHA256 over body (keyed by
// secret) matches the provided hex digest. The match is constant-time —
// `hmac.Equal` is the canonical Go API and it is constant-time per the
// stdlib docs. We hex-decode both sides before the compare so a malformed
// hex string fails before any timing-sensitive operation.
//
// Returns ErrMissingSignature, ErrMalformedSignature, or ErrBadSignature
// so the caller can map each to the right HTTP status.
func VerifySignature(secret []byte, body []byte, sigHeader string) error {
	// Syntactic pre-checks keep GitHub's finer-grained error taxonomy
	// (missing / malformed) that the shared ErrUnsigned/ErrBadSignature pair does
	// not distinguish. The constant-time digest match itself is delegated to the
	// shared webhookrecv core (byte-identical: lowercase-hex decode then
	// hmac.Equal over the raw digest bytes).
	if sigHeader == "" {
		return ErrMissingSignature
	}
	if !strings.HasPrefix(sigHeader, signaturePrefix) {
		return ErrMalformedSignature
	}
	if _, err := hex.DecodeString(strings.TrimPrefix(sigHeader, signaturePrefix)); err != nil {
		return ErrMalformedSignature
	}
	h := http.Header{}
	h.Set(HeaderSignature, sigHeader)
	if err := ghHMAC.Verify(secret, body, h); err != nil {
		return ErrBadSignature
	}
	return nil
}

// Sign returns the X-Hub-Signature-256 value computed from secret + body
// (lowercase hex behind "sha256="). Used by tests only. Production code never
// signs outbound webhooks.
func Sign(secret []byte, body []byte) string {
	return ghHMAC.Sign(secret, body)
}

// Errors returned by VerifySignature. The handler maps them to status
// codes: missing/malformed → 401, bad signature → 401, anything else →
// 500.
var (
	ErrMissingSignature   = errors.New("githubwebhook: X-Hub-Signature-256 header is missing")
	ErrMalformedSignature = errors.New("githubwebhook: X-Hub-Signature-256 is malformed")
	ErrBadSignature       = errors.New("githubwebhook: X-Hub-Signature-256 does not match")
)

// Pusher is the narrow surface the handler depends on — the cmd layer
// adapts pkg/sdk-go's Client to this.
type Pusher interface {
	Push(ctx context.Context, record *AuditEventRecord) error
}

// AuditEventRecord is the connector-internal shape between the handler
// and the cmd layer. The cmd layer transforms it into the protobuf
// EvidenceRecord (so githubwebhook stays free of proto dependencies).
type AuditEventRecord struct {
	IdempotencyKey string
	EventType      string
	Action         string
	Actor          string
	Org            string
	Repo           string
	DeliveryID     string
	CreatedAt      time.Time
	// RawPayload is the verified-original JSON body — preserved verbatim
	// so the ingest stage can hash it canonically.
	RawPayload []byte
}

// Handler is the HTTP handler. Construct via NewHandler so the secret +
// pusher invariants are enforced.
type Handler struct {
	secret []byte
	push   Pusher
	now    func() time.Time
}

// NewHandler builds a handler. Returns an error if secret is empty or
// pusher is nil — the binary must surface "you forgot to configure the
// webhook secret" loudly rather than silently accept unsigned bodies.
func NewHandler(secret []byte, pusher Pusher, now func() time.Time) (*Handler, error) {
	if len(secret) == 0 {
		return nil, errors.New("githubwebhook: secret is required")
	}
	if pusher == nil {
		return nil, errors.New("githubwebhook: pusher is required")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Handler{secret: secret, push: pusher, now: now}, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1 MiB cap
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}

	// Anti-criterion P0: signature verification is the FIRST thing after
	// reading the body. Any path that pushes without entering this branch
	// is a security regression.
	if err := VerifySignature(h.secret, body, r.Header.Get(HeaderSignature)); err != nil {
		http.Error(w, "signature invalid", http.StatusUnauthorized)
		return
	}

	delivery := strings.TrimSpace(r.Header.Get(HeaderDelivery))
	if delivery == "" {
		// Anti-criterion P0: idempotency_key is non-negotiable. Reject
		// rather than fabricate one.
		http.Error(w, "missing X-GitHub-Delivery header", http.StatusBadRequest)
		return
	}
	event := r.Header.Get(HeaderEvent)
	if event == "" {
		http.Error(w, "missing X-GitHub-Event header", http.StatusBadRequest)
		return
	}

	rec, err := transform(event, delivery, body, h.now)
	if err != nil {
		http.Error(w, "transform: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := h.push.Push(r.Context(), rec); err != nil {
		http.Error(w, "push: "+err.Error(), http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// transform decodes the verified body into the canonical AuditEventRecord.
// The fields the schema requires (action, actor, created_at, org) are
// extracted; the verbatim body is also retained for ledger-side hashing.
func transform(event, delivery string, body []byte, now func() time.Time) (*AuditEventRecord, error) {
	var w wireBody
	if err := json.Unmarshal(body, &w); err != nil {
		return nil, fmt.Errorf("decode body: %w", err)
	}
	rec := &AuditEventRecord{
		IdempotencyKey: delivery, // anti-criterion P0: derived from delivery id
		EventType:      event,
		Action:         w.Action,
		Actor:          w.Sender.Login,
		Org:            w.Organization.Login,
		Repo:           w.Repository.FullName,
		DeliveryID:     delivery,
		CreatedAt:      now(),
		RawPayload:     body,
	}
	if rec.Action == "" {
		// Some delivery shapes don't carry action (e.g. ping). Use the
		// event type as a stable proxy.
		rec.Action = event
	}
	if rec.Actor == "" {
		rec.Actor = "unknown"
	}
	if rec.Org == "" {
		return nil, errors.New("organization.login missing — slice 044 requires org-scoped webhooks")
	}
	return rec, nil
}

// wireBody is the subset of GitHub's webhook delivery we depend on.
type wireBody struct {
	Action string `json:"action"`
	Sender struct {
		Login string `json:"login"`
	} `json:"sender"`
	Organization struct {
		Login string `json:"login"`
	} `json:"organization"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
}
