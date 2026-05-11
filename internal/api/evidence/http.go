package evidence

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/evidence/ingest"
)

// HTTPHandler serves the slice-013 push API:
//
//	POST /v1/evidence:push       single record or batch (<=100)
//
// Auth is enforced upstream by httpAuthMiddleware; the handler additionally
// reads the credential from context for ingestion-side scope checks.
type HTTPHandler struct {
	svc     *ingest.Service
	limiter *tokenBucketRegistry
	// maxBatch caps the batch size at 100 per EVIDENCE_SDK §4.1.
	maxBatch int
	// maxBodyBytes caps the inbound body. The push profile inline limit
	// is 1 MiB per record (AC-6); for batches up to 100, the body cap is
	// 100 MiB. We start with a 2 MiB single-request cap and bump on
	// batch — but for v1 we trust the per-record check inside Process
	// and cap the body at 100 MiB to keep memory bounded.
	maxBodyBytes int64
}

// NewHTTPHandler constructs the handler. limiterPerSecond is the default
// rate-limit token-bucket replenish rate per credential; pass 0 to
// disable rate limiting (used by unit tests).
func NewHTTPHandler(svc *ingest.Service, limiterPerSecond float64) *HTTPHandler {
	return &HTTPHandler{
		svc:          svc,
		limiter:      newTokenBucketRegistry(limiterPerSecond, limiterPerSecond*2),
		maxBatch:     100,
		maxBodyBytes: 100 << 20, // 100 MiB
	}
}

// pushRequest is the wire body for POST /v1/evidence:push. Accepts either
// a single record (records: [...]) or a single record at the top level.
// The proto record is mapped to a wire shape that omits server-set fields.
type pushRequest struct {
	Records []recordWire `json:"records,omitempty"`
	// Single-record shorthand: callers may post one record at the top
	// level without wrapping in `records`. The handler upgrades a
	// non-empty top-level record into a single-element batch.
	Record *recordWire `json:"record,omitempty"`
}

type recordWire struct {
	IdempotencyKey    string                `json:"idempotency_key"`
	EvidenceKind      string                `json:"evidence_kind"`
	SchemaVersion     string                `json:"schema_version"`
	ControlID         string                `json:"control_id"`
	Scope             []scopeDimensionWire  `json:"scope"`
	ObservedAt        string                `json:"observed_at"`
	Result            string                `json:"result"`
	Payload           map[string]any        `json:"payload"`
	PayloadURI        *string               `json:"payload_uri,omitempty"`
	SourceAttribution sourceAttributionWire `json:"source_attribution"`
}

type scopeDimensionWire struct {
	Key    string   `json:"key"`
	Values []string `json:"values"`
}

type sourceAttributionWire struct {
	ActorType string `json:"actor_type"`
	ActorID   string `json:"actor_id"`
	SessionID string `json:"session_id,omitempty"`
}

type receiptWire struct {
	RecordID     string `json:"record_id"`
	Hash         string `json:"hash"`
	IngestedAt   string `json:"ingested_at"`
	CredentialID string `json:"credential_id"`
	Deduplicated bool   `json:"deduplicated"`
}

type pushResponse struct {
	Receipts []receiptWire `json:"receipts"`
}

type errorBody struct {
	Error string `json:"error"`
	Code  string `json:"code,omitempty"`
}

// PushHTTP is the canonical push handler. AC-1, AC-2, AC-3, AC-4, AC-5,
// AC-7, AC-8, AC-9 all run through here.
func (h *HTTPHandler) PushHTTP(w http.ResponseWriter, r *http.Request) {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, errorBody{Error: "authentication required"})
		return
	}

	// AC-5: rate limit per credential. Honors Retry-After.
	if h.limiter != nil {
		if wait := h.limiter.take(cred.ID); wait > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds()+0.5)))
			writeJSON(w, http.StatusTooManyRequests, errorBody{Error: "rate limit exceeded", Code: "rate_limited"})
			return
		}
	}

	// Bound the body so a malicious caller can't OOM us.
	r.Body = http.MaxBytesReader(w, r.Body, h.maxBodyBytes)

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge, errorBody{Error: "body too large or unreadable: " + err.Error(), Code: "oversized"})
		return
	}
	if len(body) == 0 {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "request body is empty"})
		return
	}

	var req pushRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "invalid JSON body: " + err.Error()})
		return
	}

	// Normalize to a batch.
	batch := req.Records
	if req.Record != nil {
		batch = append([]recordWire{*req.Record}, batch...)
	}
	if len(batch) == 0 {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "no records in request"})
		return
	}
	if len(batch) > h.maxBatch {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: fmt.Sprintf("batch exceeds %d records", h.maxBatch)})
		return
	}

	receipts := make([]receiptWire, 0, len(batch))
	for i, rec := range batch {
		proto, perr := recordWireToProto(rec)
		if perr != nil {
			writeJSON(w, http.StatusBadRequest, errorBody{Error: fmt.Sprintf("record[%d]: %s", i, perr.Error())})
			return
		}
		receipt, decision, err := h.svc.Process(r.Context(), proto, cred)
		if err != nil {
			writeBatchError(w, i, decision, err)
			return
		}
		receipts = append(receipts, receiptWire{
			RecordID:     receipt.RecordID,
			Hash:         receipt.Hash,
			IngestedAt:   receipt.IngestedAt.Format(time.RFC3339Nano),
			CredentialID: receipt.CredentialID,
			Deduplicated: receipt.Deduplicated,
		})
	}

	// Single-record shape returns a top-level receipt; batch returns a
	// list. The single-record path returns 201; the batch path returns
	// 200 because a 201 implies "one resource created".
	if len(receipts) == 1 && req.Record != nil && len(req.Records) == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(receipts[0])
		return
	}
	writeJSON(w, http.StatusOK, pushResponse{Receipts: receipts})
}

// writeBatchError maps an ingest decision to an HTTP status code and
// surfaces a structured error body. The decision is also written to the
// audit log inside Process; this function only translates for the
// client.
func writeBatchError(w http.ResponseWriter, index int, decision ingest.Decision, err error) {
	statusCode := http.StatusInternalServerError
	code := "internal_error"
	switch {
	case errors.Is(err, ingest.ErrMissingField):
		statusCode = http.StatusBadRequest
		code = "missing_field"
	case errors.Is(err, ingest.ErrUnknownKind):
		statusCode = http.StatusPreconditionFailed
		code = "unknown_evidence_kind"
	case errors.Is(err, ingest.ErrValidation):
		statusCode = http.StatusBadRequest
		code = "schema_validation_failed"
	case errors.Is(err, ingest.ErrIdempotencyMismatch):
		statusCode = http.StatusConflict
		code = "idempotency_mismatch"
	case errors.Is(err, ingest.ErrScopeViolation):
		statusCode = http.StatusForbidden
		code = "scope_violation"
	case errors.Is(err, ingest.ErrObservedAtSkew):
		statusCode = http.StatusBadRequest
		code = "observed_at_skew"
	case errors.Is(err, ingest.ErrOversized):
		statusCode = http.StatusRequestEntityTooLarge
		code = "oversized_payload"
	}
	writeJSON(w, statusCode, errorBody{
		Error: fmt.Sprintf("record[%d] %s: %s", index, decision.String(), err.Error()),
		Code:  code,
	})
}

// recordWireToProto converts the wire shape into the slice-003 proto
// message that ingest.Service.Process accepts. Bad-shape conversions
// surface as ingest.ErrMissingField semantics — but the HTTP layer is
// careful to map decode-level errors to 400 separately so callers can
// distinguish "your JSON was malformed" from "your record was rejected".
func recordWireToProto(w recordWire) (*evidencev1.EvidenceRecord, error) {
	scope := make([]*evidencev1.ScopeDimension, len(w.Scope))
	for i, d := range w.Scope {
		if d.Key == "" {
			return nil, fmt.Errorf("scope[%d].key required", i)
		}
		if len(d.Values) == 0 {
			return nil, fmt.Errorf("scope[%d].values required", i)
		}
		scope[i] = &evidencev1.ScopeDimension{Key: d.Key, Values: d.Values}
	}

	var observed *timestamppb.Timestamp
	if w.ObservedAt != "" {
		t, err := time.Parse(time.RFC3339Nano, w.ObservedAt)
		if err != nil {
			t, err = time.Parse(time.RFC3339, w.ObservedAt)
			if err != nil {
				return nil, fmt.Errorf("observed_at must be RFC3339: %v", err)
			}
		}
		observed = timestamppb.New(t)
	}

	payloadStruct, err := structpb.NewStruct(w.Payload)
	if err != nil {
		return nil, fmt.Errorf("payload not convertible to struct: %v", err)
	}

	result, ok := wireResultToProto(w.Result)
	if !ok {
		return nil, fmt.Errorf("result must be one of pass|fail|na|inconclusive")
	}

	return &evidencev1.EvidenceRecord{
		IdempotencyKey: w.IdempotencyKey,
		EvidenceKind:   w.EvidenceKind,
		SchemaVersion:  w.SchemaVersion,
		ControlId:      w.ControlID,
		Scope:          scope,
		ObservedAt:     observed,
		Result:         result,
		Payload:        payloadStruct,
		PayloadUri:     w.PayloadURI,
		SourceAttribution: &evidencev1.SourceAttribution{
			ActorType: w.SourceAttribution.ActorType,
			ActorId:   w.SourceAttribution.ActorID,
			SessionId: w.SourceAttribution.SessionID,
		},
	}, nil
}

func wireResultToProto(s string) (evidencev1.Result, bool) {
	switch s {
	case "pass":
		return evidencev1.Result_RESULT_PASS, true
	case "fail":
		return evidencev1.Result_RESULT_FAIL, true
	case "na":
		return evidencev1.Result_RESULT_NA, true
	case "inconclusive":
		return evidencev1.Result_RESULT_INCONCLUSIVE, true
	}
	return evidencev1.Result_RESULT_UNSPECIFIED, false
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// ---- token bucket rate limiter ----
//
// Per-credential token bucket. The bucket holds `burst` tokens and
// refills at `ratePerSecond`. take() returns the duration the caller
// would need to wait if the bucket is empty; zero means proceed.

type tokenBucketRegistry struct {
	mu       sync.Mutex
	buckets  map[string]*tokenBucket
	rate     float64
	capacity float64
	now      func() time.Time
}

type tokenBucket struct {
	tokens float64
	last   time.Time
}

func newTokenBucketRegistry(rate, capacity float64) *tokenBucketRegistry {
	if rate <= 0 || capacity <= 0 {
		return nil
	}
	return &tokenBucketRegistry{
		buckets:  map[string]*tokenBucket{},
		rate:     rate,
		capacity: capacity,
		now:      func() time.Time { return time.Now() },
	}
}

// take returns 0 on success, or the duration the caller should wait
// (Retry-After) when the bucket is empty.
func (r *tokenBucketRegistry) take(key string) time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	b, ok := r.buckets[key]
	if !ok {
		b = &tokenBucket{tokens: r.capacity, last: now}
		r.buckets[key] = b
	}
	elapsed := now.Sub(b.last).Seconds()
	b.tokens += elapsed * r.rate
	if b.tokens > r.capacity {
		b.tokens = r.capacity
	}
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return 0
	}
	// Tokens needed: 1 - b.tokens; at r.rate tokens/sec.
	wait := (1 - b.tokens) / r.rate
	return time.Duration(wait * float64(time.Second))
}
