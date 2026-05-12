// Package ingest is the ingestion stage of the evidence pipeline. It
// canonicalizes, hashes, schema-validates, scope-tags, and writes
// EvidenceRecord values to the append-only ledger.
//
// Constitutional invariant #2 (canvas §4.3): ingestion and evaluation are
// separated stages with an append-only evidence ledger between them. The
// ingest stage NEVER reads from the evaluation stage and the evaluation
// stage NEVER writes through this package. Slice 015 will swap the
// invocation substrate underneath this package (in-process call → NATS
// JetStream publish) without changing the package boundary or the
// Service.Process signature; the integration test in slice 013 asserts
// that boundary up-front.
//
// The package is intentionally substrate-agnostic: it does not import the
// HTTP/gRPC layer, knows nothing about transports, and has one external
// dependency for validation (the schema registry) plus the database.
// That isolation is what makes the slice-015 substrate swap mechanical.
package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/canonjson"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/evidence/redact"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// pgErrUniqueViolation is the SQLSTATE for a UNIQUE constraint trip.
// The idempotency partial-unique index trips here on a same-key, same-
// transaction race; we collapse that to "the record was just inserted
// by another in-flight push and is now deduplicated".
const pgErrUniqueViolation = "23505"

// MaxObservedAtSkew bounds how far the caller's observed_at may stray
// from received_at. EVIDENCE_SDK §9 calls this out as the replay-protection
// mitigation. Symmetric: past and future skew both rejected.
const MaxObservedAtSkew = 24 * time.Hour

// MaxPayloadBytes caps the inline payload size. EVIDENCE_SDK §4.6 and
// AC-6 redirect payloads above this to S3 (slice 036). Until slice 036
// lands, the ingestion layer rejects oversized inline payloads with an
// explicit error pointing the caller at payload_uri.
const MaxPayloadBytes = 1 << 20 // 1 MiB

// SchemaValidator is the validation hook into slice 014. Service.Process
// calls ValidatePayload for every record before any DB write. The
// signature mirrors `schemaregistry.Service.ValidatePayload`.
type SchemaValidator interface {
	ValidatePayload(ctx context.Context, tenantID, kind, version string, payload []byte) error
	IsRegistered(kind, version string) bool
}

// RedactionLookup is the optional slice-015 hook into the schema
// registry: given a (kind, semver), return the JSONPath rules declared
// under the schema's `x-redaction-rules` extension key. A validator
// that does not implement this interface is treated as "no rules"
// (the slice-014 InMemory registry, for example).
type RedactionLookup interface {
	RedactionRulesFor(ctx context.Context, tenantID, kind, version string) ([]string, error)
}

// Decision enumerates the outcome of Service.Process. Every value maps to
// a row in evidence_audit_log; every value also maps to a status code at
// the HTTP / gRPC handler layer.
type Decision int

const (
	DecisionUnknown Decision = iota
	DecisionAccepted
	DecisionDeduplicated
	DecisionRejectedValidation
	DecisionRejectedUnknownKind
	DecisionRejectedIdempotencyMismatch
	DecisionRejectedScopeViolation
	DecisionRejectedObservedAtSkew
	DecisionRejectedOversized
	DecisionRejectedUnauthenticated
	DecisionRejectedRateLimit
	DecisionRejectedInternalError
)

// String returns the audit-log decision token for d. Must match the
// evidence_audit_log_decision_chk CHECK constraint.
func (d Decision) String() string {
	switch d {
	case DecisionAccepted:
		return "accepted"
	case DecisionDeduplicated:
		return "deduplicated"
	case DecisionRejectedValidation:
		return "rejected_validation"
	case DecisionRejectedUnknownKind:
		return "rejected_unknown_kind"
	case DecisionRejectedIdempotencyMismatch:
		return "rejected_idempotency_mismatch"
	case DecisionRejectedScopeViolation:
		return "rejected_scope_violation"
	case DecisionRejectedObservedAtSkew:
		return "rejected_observed_at_skew"
	case DecisionRejectedOversized:
		return "rejected_oversized"
	case DecisionRejectedUnauthenticated:
		return "rejected_unauthenticated"
	case DecisionRejectedRateLimit:
		return "rejected_rate_limit"
	default:
		return "rejected_internal_error"
	}
}

// Error categories the handler layer routes on. Each ties to a Decision
// and an HTTP status / gRPC code at the boundary; see the HTTP handler
// for the mapping.
var (
	ErrMissingField        = errors.New("ingest: required field missing")
	ErrUnknownKind         = errors.New("ingest: evidence_kind not registered")
	ErrValidation          = errors.New("ingest: payload failed schema validation")
	ErrIdempotencyMismatch = errors.New("ingest: idempotency_key reused with different content")
	ErrScopeViolation      = errors.New("ingest: credential not authorized for this push")
	ErrObservedAtSkew      = errors.New("ingest: observed_at outside permitted skew window")
	ErrOversized           = errors.New("ingest: payload exceeds inline size limit; use payload_uri")
)

// Receipt is the outcome of a successful (accepted or deduplicated)
// push. The caller (HTTP/gRPC handler) returns it to the pusher; the
// hash is the canonjson sha256 of the record.
type Receipt struct {
	RecordID     string
	Hash         string
	IngestedAt   time.Time
	CredentialID string
	Deduplicated bool
}

// Service holds the dependencies of the ingestion stage. The pool MUST
// be the application-role pool (atlas_app NOSUPERUSER NOBYPASSRLS) so
// RLS policies are enforced; the migration role can BYPASSRLS and would
// hide tenant-isolation bugs.
type Service struct {
	pool  *pgxpool.Pool
	valid SchemaValidator
	clock func() time.Time
	// path is the ingestion_path tag written on every record. Defaults
	// to "push" — connector / webhook callers can construct a Service
	// with a different path tag via WithPath.
	path string
}

// New constructs a Service over the supplied pool and validator. Pass
// nil clock for time.Now.
func New(pool *pgxpool.Pool, validator SchemaValidator) *Service {
	if pool == nil {
		panic("ingest: pool is required")
	}
	if validator == nil {
		panic("ingest: validator is required")
	}
	return &Service{
		pool:  pool,
		valid: validator,
		clock: func() time.Time { return time.Now().UTC() },
		path:  "push",
	}
}

// WithClock returns a copy of s with the supplied clock. Tests pin a
// deterministic clock so observed_at-skew assertions don't depend on
// wall-clock drift.
func (s *Service) WithClock(clock func() time.Time) *Service {
	copy := *s
	if clock != nil {
		copy.clock = clock
	}
	return &copy
}

// WithPath returns a copy of s with the supplied ingestion_path tag. The
// canvas distinguishes push / pull / subscribe / webhook / manual_upload
// at the ledger row (canvas §4.7) so the audit trail can answer "where
// did this evidence come from?" precisely.
func (s *Service) WithPath(path string) *Service {
	copy := *s
	copy.path = path
	return &copy
}

// Process runs the ingestion stage for one EvidenceRecord:
//
//  1. Validate every required proto field (AC-2)
//  2. Validate observed_at is within MaxObservedAtSkew of received_at (AC-8)
//  3. Reject oversized inline payloads (AC-6 partial — slice 036 pending)
//  4. Reject unknown evidence_kind via the schema registry hook (AC-9)
//  5. Schema-validate payload via the registry hook
//  6. Enforce credential.Kinds and credential.ScopePredicate
//  7. sha256 the canonicalized record
//  8. Look up by (tenant_id, idempotency_key) — return the original
//     receipt on dedup (AC-3), or reject 409 on hash mismatch (AC-4)
//  9. Append-only INSERT into evidence_records inside a tenant-GUC tx
//
// The decision is also written to evidence_audit_log on every code path
// — accepted, deduplicated, OR rejected — keyed by credential id (AC-7).
//
// Process is the boundary slice 015 will preserve. The function does NOT
// call into any transport layer or any evaluation code.
func (s *Service) Process(ctx context.Context, rec *evidencev1.EvidenceRecord, cred credstore.Credential) (Receipt, Decision, error) {
	receivedAt := s.clock()

	if rec == nil {
		s.writeAudit(ctx, cred, "", "", DecisionRejectedValidation, "nil record", pgtype.UUID{})
		return Receipt{}, DecisionRejectedValidation, fmt.Errorf("%w: record is nil", ErrMissingField)
	}

	// Anonymous credential (no tenant) must never reach Process; the
	// auth middleware rejects unauthenticated requests upstream. Defensive
	// belt-and-braces check.
	if cred.TenantID == "" {
		return Receipt{}, DecisionRejectedUnauthenticated, fmt.Errorf("ingest: credential has no tenant")
	}

	if msg := missingField(rec); msg != "" {
		s.writeAudit(ctx, cred, rec.GetIdempotencyKey(), rec.GetEvidenceKind(), DecisionRejectedValidation, msg, pgtype.UUID{})
		return Receipt{}, DecisionRejectedValidation, fmt.Errorf("%w: %s", ErrMissingField, msg)
	}

	// AC-8: observed_at skew check (replay protection).
	observed := rec.GetObservedAt().AsTime()
	skew := observed.Sub(receivedAt)
	if skew < -MaxObservedAtSkew || skew > MaxObservedAtSkew {
		s.writeAudit(ctx, cred, rec.IdempotencyKey, rec.EvidenceKind, DecisionRejectedObservedAtSkew,
			fmt.Sprintf("observed_at skew %s > %s", skew, MaxObservedAtSkew), pgtype.UUID{})
		return Receipt{}, DecisionRejectedObservedAtSkew, fmt.Errorf("%w: skew=%s limit=%s", ErrObservedAtSkew, skew, MaxObservedAtSkew)
	}

	// AC-6 partial: oversize gate (slice 036 will redirect to S3).
	payloadJSON, err := protojson.Marshal(rec.GetPayload())
	if err != nil {
		s.writeAudit(ctx, cred, rec.IdempotencyKey, rec.EvidenceKind, DecisionRejectedValidation, "payload marshal: "+err.Error(), pgtype.UUID{})
		return Receipt{}, DecisionRejectedValidation, fmt.Errorf("%w: payload marshal: %v", ErrValidation, err)
	}
	if len(payloadJSON) > MaxPayloadBytes && rec.PayloadUri == nil {
		s.writeAudit(ctx, cred, rec.IdempotencyKey, rec.EvidenceKind, DecisionRejectedOversized,
			fmt.Sprintf("payload %d > %d bytes", len(payloadJSON), MaxPayloadBytes), pgtype.UUID{})
		return Receipt{}, DecisionRejectedOversized, fmt.Errorf("%w: %d > %d bytes", ErrOversized, len(payloadJSON), MaxPayloadBytes)
	}

	// AC-1 + anti-criterion: schemaless push rejected via slice-014 hook.
	if !s.valid.IsRegistered(rec.EvidenceKind, rec.SchemaVersion) {
		s.writeAudit(ctx, cred, rec.IdempotencyKey, rec.EvidenceKind, DecisionRejectedUnknownKind,
			fmt.Sprintf("kind=%s version=%s", rec.EvidenceKind, rec.SchemaVersion), pgtype.UUID{})
		return Receipt{}, DecisionRejectedUnknownKind, fmt.Errorf("%w: %s/%s", ErrUnknownKind, rec.EvidenceKind, rec.SchemaVersion)
	}
	if err := s.valid.ValidatePayload(ctx, cred.TenantID, rec.EvidenceKind, rec.SchemaVersion, payloadJSON); err != nil {
		s.writeAudit(ctx, cred, rec.IdempotencyKey, rec.EvidenceKind, DecisionRejectedValidation, err.Error(), pgtype.UUID{})
		return Receipt{}, DecisionRejectedValidation, fmt.Errorf("%w: %v", ErrValidation, err)
	}

	// AC-6 (slice 015): apply per-kind redaction rules BEFORE hashing so
	// the ledger never stores the unredacted payload and idempotency
	// dedup compares hashes of redacted forms (deterministic given the
	// same rules). When the validator does not implement RedactionLookup,
	// we treat it as "no rules" — this keeps the slice-013 InMemory
	// fallback unchanged.
	//
	// Anti-criterion (P0): we never log the raw payloadJSON here. The
	// only error path through this block surfaces the rule string or
	// the redactor's own error, not the payload.
	if lookup, ok := s.valid.(RedactionLookup); ok {
		rules, rerr := lookup.RedactionRulesFor(ctx, cred.TenantID, rec.EvidenceKind, rec.SchemaVersion)
		if rerr != nil {
			s.writeAudit(ctx, cred, rec.IdempotencyKey, rec.EvidenceKind, DecisionRejectedInternalError,
				"redaction lookup: "+rerr.Error(), pgtype.UUID{})
			return Receipt{}, DecisionRejectedInternalError, fmt.Errorf("ingest: redaction lookup: %w", rerr)
		}
		if len(rules) > 0 {
			redacted, aerr := redact.Apply(rec.Payload, rules)
			if aerr != nil {
				s.writeAudit(ctx, cred, rec.IdempotencyKey, rec.EvidenceKind, DecisionRejectedInternalError,
					"redaction apply: "+aerr.Error(), pgtype.UUID{})
				return Receipt{}, DecisionRejectedInternalError, fmt.Errorf("ingest: redaction apply: %w", aerr)
			}
			rec.Payload = redacted
			// Re-marshal so payloadJSON (used below for the DB write) is
			// the redacted form. Hashing happens below — it will hash
			// the redacted record. The unredacted form is GC'd here.
			redactedJSON, merr := protojson.Marshal(redacted)
			if merr != nil {
				s.writeAudit(ctx, cred, rec.IdempotencyKey, rec.EvidenceKind, DecisionRejectedInternalError,
					"payload re-marshal after redact", pgtype.UUID{})
				return Receipt{}, DecisionRejectedInternalError, fmt.Errorf("ingest: redacted re-marshal: %w", merr)
			}
			payloadJSON = redactedJSON
		}
	}

	// Credential scope enforcement: AC anti-criterion "no cross-credential
	// kind escalation". A credential issued with a non-empty Kinds list
	// may only push records of those kinds. Empty Kinds means "any kind"
	// (legacy bootstrap credential).
	if len(cred.Kinds) > 0 && !contains(cred.Kinds, rec.EvidenceKind) {
		s.writeAudit(ctx, cred, rec.IdempotencyKey, rec.EvidenceKind, DecisionRejectedScopeViolation,
			fmt.Sprintf("credential %s not authorized for kind %s", cred.ID, rec.EvidenceKind), pgtype.UUID{})
		return Receipt{}, DecisionRejectedScopeViolation, fmt.Errorf("%w: kind=%s", ErrScopeViolation, rec.EvidenceKind)
	}

	// Credential scope_predicate: a tiny key=value subset enforcement for
	// v1. EVIDENCE_SDK §4.3 specifies that a credential can be scoped to
	// e.g. `cloud_account=aws:111122223333`. Parse predicate and require
	// the record's scope to contain those values.
	if cred.ScopePredicate != "" {
		if !scopeSatisfiesPredicate(rec.GetScope(), cred.ScopePredicate) {
			s.writeAudit(ctx, cred, rec.IdempotencyKey, rec.EvidenceKind, DecisionRejectedScopeViolation,
				fmt.Sprintf("scope_predicate %q not satisfied", cred.ScopePredicate), pgtype.UUID{})
			return Receipt{}, DecisionRejectedScopeViolation, fmt.Errorf("%w: predicate=%q", ErrScopeViolation, cred.ScopePredicate)
		}
	}

	// Hash the canonicalized record (canonjson handles deterministic
	// proto serialization).
	hash, err := canonjson.HashRecord(rec)
	if err != nil {
		s.writeAudit(ctx, cred, rec.IdempotencyKey, rec.EvidenceKind, DecisionRejectedInternalError, "canonjson: "+err.Error(), pgtype.UUID{})
		return Receipt{}, DecisionRejectedInternalError, fmt.Errorf("ingest: hash: %w", err)
	}

	// Provenance JSONB. EVIDENCE_SDK §4.7 lists the canonical fields. We
	// record what the server observed, not what the client claimed (the
	// client claim is on rec.SourceAttribution and is preserved separately).
	provenance := map[string]any{
		"ingestion_path":   s.path,
		"credential_id":    cred.ID,
		"credential_type":  "api_key", // OIDC/mTLS land in v2
		"received_at":      receivedAt.Format(time.RFC3339Nano),
		"endpoint_version": "v1",
		"schema_version":   rec.SchemaVersion,
	}
	provenanceBytes, _ := json.Marshal(provenance)

	sourceAttribJSON, _ := json.Marshal(map[string]any{
		"actor_type": rec.GetSourceAttribution().GetActorType(),
		"actor_id":   rec.GetSourceAttribution().GetActorId(),
		"session_id": rec.GetSourceAttribution().GetSessionId(),
	})

	// Try parsing control_id as a UUID. If it parses, set both columns.
	// If not (e.g., "scf:VPM-04"), leave control_id NULL and set
	// control_ref only.
	var controlID pgtype.UUID
	if u, perr := uuid.Parse(rec.ControlId); perr == nil {
		controlID = pgtype.UUID{Bytes: u, Valid: true}
	}

	// Map the proto Result to the SQL enum value.
	resultEnum, ok := protoResultToEnum(rec.Result)
	if !ok {
		s.writeAudit(ctx, cred, rec.IdempotencyKey, rec.EvidenceKind, DecisionRejectedValidation,
			"unrecognized result enum", pgtype.UUID{})
		return Receipt{}, DecisionRejectedValidation, fmt.Errorf("%w: result=%s", ErrMissingField, rec.Result)
	}

	// The DB write runs inside a transaction with the tenant GUC set so
	// RLS allows the INSERT (tenant_insert WITH CHECK). The idempotency
	// dedup also happens inside that tx so a same-key concurrent push
	// either observes the existing row OR trips the UNIQUE index.
	tenantCtx, terr := tenancy.WithTenant(ctx, cred.TenantID)
	if terr != nil {
		s.writeAudit(ctx, cred, rec.IdempotencyKey, rec.EvidenceKind, DecisionRejectedInternalError, "WithTenant: "+terr.Error(), pgtype.UUID{})
		return Receipt{}, DecisionRejectedInternalError, fmt.Errorf("ingest: tenant context: %w", terr)
	}

	var newRecord dbx.EvidenceRecord
	var deduped bool

	err = pgx.BeginTxFunc(tenantCtx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if terr := tenancy.ApplyTenant(tenantCtx, tx); terr != nil {
			return fmt.Errorf("apply tenant: %w", terr)
		}
		q := dbx.New(tx)

		// AC-3 / AC-4: idempotency lookup.
		idemKey := rec.IdempotencyKey
		existing, lerr := q.GetEvidenceRecordByIdempotency(tenantCtx, dbx.GetEvidenceRecordByIdempotencyParams{
			TenantID:       pgUUID(cred.TenantID),
			IdempotencyKey: &idemKey,
		})
		if lerr == nil {
			// A row already exists for this idempotency_key. Either the
			// hash matches (deduplicate, return original receipt) or it
			// doesn't (reject with 409).
			if existing.Hash != hash {
				return ErrIdempotencyMismatch
			}
			newRecord = existing
			deduped = true
			return nil
		} else if !errors.Is(lerr, pgx.ErrNoRows) {
			return fmt.Errorf("idempotency lookup: %w", lerr)
		}

		// AC-1: append the record.
		recordID := pgUUID(uuid.New().String())
		validUntil := pgtype.Timestamptz{}

		params := dbx.InsertEvidenceRecordParams{
			ID:                recordID,
			TenantID:          pgUUID(cred.TenantID),
			ControlID:         controlID,
			ControlRef:        rec.ControlId,
			ScopeID:           pgtype.UUID{},
			ObservedAt:        pgtype.Timestamptz{Time: observed, Valid: true},
			Provenance:        provenanceBytes,
			Result:            resultEnum,
			Payload:           payloadJSON,
			PayloadUri:        rec.PayloadUri,
			Hash:              hash,
			FreshnessClass:    dbx.EvidenceFreshnessClassMonthly, // default; tightened by control bundle in slice 016
			ValidUntil:        validUntil,
			IdempotencyKey:    &idemKey,
			EvidenceKind:      ptrStr(rec.EvidenceKind),
			SchemaVersion:     ptrStr(rec.SchemaVersion),
			CredentialID:      ptrStr(cred.ID),
			IngestionPath:     s.path,
			SourceAttribution: sourceAttribJSON,
		}
		inserted, ierr := q.InsertEvidenceRecord(tenantCtx, params)
		if ierr != nil {
			// Race: a concurrent push with the same idempotency_key won.
			// Re-fetch and treat as dedup if the hash matches.
			var pgErr *pgconn.PgError
			if errors.As(ierr, &pgErr) && pgErr.Code == pgErrUniqueViolation {
				existing, gerr := q.GetEvidenceRecordByIdempotency(tenantCtx, dbx.GetEvidenceRecordByIdempotencyParams{
					TenantID:       pgUUID(cred.TenantID),
					IdempotencyKey: &idemKey,
				})
				if gerr == nil {
					if existing.Hash != hash {
						return ErrIdempotencyMismatch
					}
					newRecord = existing
					deduped = true
					return nil
				}
			}
			return fmt.Errorf("insert evidence: %w", ierr)
		}
		newRecord = inserted
		return nil
	})

	if err != nil {
		if errors.Is(err, ErrIdempotencyMismatch) {
			s.writeAudit(ctx, cred, rec.IdempotencyKey, rec.EvidenceKind, DecisionRejectedIdempotencyMismatch,
				"hash mismatch for idempotency_key", pgtype.UUID{})
			return Receipt{}, DecisionRejectedIdempotencyMismatch, ErrIdempotencyMismatch
		}
		s.writeAudit(ctx, cred, rec.IdempotencyKey, rec.EvidenceKind, DecisionRejectedInternalError, err.Error(), pgtype.UUID{})
		return Receipt{}, DecisionRejectedInternalError, err
	}

	decision := DecisionAccepted
	if deduped {
		decision = DecisionDeduplicated
	}
	s.writeAudit(ctx, cred, rec.IdempotencyKey, rec.EvidenceKind, decision, "", newRecord.ID)

	return Receipt{
		RecordID:     uuid.UUID(newRecord.ID.Bytes).String(),
		Hash:         newRecord.Hash,
		IngestedAt:   newRecord.IngestedAt.Time,
		CredentialID: cred.ID,
		Deduplicated: deduped,
	}, decision, nil
}

// writeAudit lands one row in evidence_audit_log. Best-effort: a failure
// here is logged via the returned error from Process if relevant, but
// never blocks the calling decision (we already reached a verdict).
//
// The write runs in its own short-lived transaction with the tenant GUC
// set so RLS allows the INSERT. If the caller has no tenant — e.g., the
// auth interceptor rejected before Process ran — the audit row is
// silently dropped (callers of Process always have a tenant; the
// belt-and-braces "no tenant" branch returns before writeAudit fires).
func (s *Service) writeAudit(ctx context.Context, cred credstore.Credential, idem, kind string, decision Decision, reason string, recordID pgtype.UUID) {
	if cred.TenantID == "" {
		return
	}
	tenantCtx, terr := tenancy.WithTenant(context.WithoutCancel(ctx), cred.TenantID)
	if terr != nil {
		return
	}
	_ = pgx.BeginTxFunc(tenantCtx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if terr := tenancy.ApplyTenant(tenantCtx, tx); terr != nil {
			return terr
		}
		q := dbx.New(tx)
		var idemPtr, kindPtr *string
		if idem != "" {
			idemPtr = &idem
		}
		if kind != "" {
			kindPtr = &kind
		}
		_, _ = q.InsertEvidenceAuditEntry(tenantCtx, dbx.InsertEvidenceAuditEntryParams{
			ID:             pgUUID(uuid.New().String()),
			TenantID:       pgUUID(cred.TenantID),
			CredentialID:   cred.ID,
			Decision:       decision.String(),
			ReasonCode:     truncate(reason, 1024),
			IdempotencyKey: idemPtr,
			EvidenceKind:   kindPtr,
			RecordID:       recordID,
		})
		return nil
	})
}

// missingField mirrors slice 003's check; duplicating it here keeps the
// ingest package self-contained against future proto evolution.
func missingField(r *evidencev1.EvidenceRecord) string {
	switch {
	case r.GetIdempotencyKey() == "":
		return "idempotency_key is required"
	case r.GetEvidenceKind() == "":
		return "evidence_kind is required"
	case r.GetSchemaVersion() == "":
		return "schema_version is required"
	case r.GetControlId() == "":
		return "control_id is required"
	case len(r.GetScope()) == 0:
		return "scope is required (at least one dimension)"
	case r.GetObservedAt() == nil:
		return "observed_at is required"
	case r.GetResult() == evidencev1.Result_RESULT_UNSPECIFIED:
		return "result is required"
	case r.GetPayload() == nil:
		return "payload is required"
	case r.GetSourceAttribution() == nil:
		return "source_attribution is required"
	}
	for _, d := range r.GetScope() {
		if d.GetKey() == "" {
			return "scope dimension key is required"
		}
		if len(d.GetValues()) == 0 {
			return "scope dimension values is required"
		}
	}
	return ""
}

// scopeSatisfiesPredicate is the v1 scope_predicate evaluator. The
// predicate format is "key=value[,key=value...]" — every key/value pair
// must appear in the record's scope. EVIDENCE_SDK §4.3 anticipates a
// richer expression language in v2; v1 hard-codes simple equality so the
// security boundary is auditable.
func scopeSatisfiesPredicate(scope []*evidencev1.ScopeDimension, predicate string) bool {
	have := map[string]map[string]bool{}
	for _, d := range scope {
		have[d.Key] = map[string]bool{}
		for _, v := range d.Values {
			have[d.Key][v] = true
		}
	}
	for _, part := range strings.Split(predicate, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			return false
		}
		k := strings.TrimSpace(kv[0])
		v := strings.TrimSpace(kv[1])
		vals, ok := have[k]
		if !ok || !vals[v] {
			return false
		}
	}
	return true
}

// protoResultToEnum maps the proto Result enum to the SQL evidence_result
// enum. Returns ok=false on RESULT_UNSPECIFIED (caller treats as missing
// field, which the upstream missingField check already covers).
func protoResultToEnum(r evidencev1.Result) (dbx.EvidenceResult, bool) {
	switch r {
	case evidencev1.Result_RESULT_PASS:
		return dbx.EvidenceResultPass, true
	case evidencev1.Result_RESULT_FAIL:
		return dbx.EvidenceResultFail, true
	case evidencev1.Result_RESULT_NA:
		return dbx.EvidenceResultNa, true
	case evidencev1.Result_RESULT_INCONCLUSIVE:
		return dbx.EvidenceResultInconclusive, true
	}
	return dbx.EvidenceResultInconclusive, false
}

// ReceiptToProto returns the proto-shaped receipt the API handler returns.
func ReceiptToProto(r Receipt) *evidencev1.EvidenceReceipt {
	return &evidencev1.EvidenceReceipt{
		RecordId:     r.RecordID,
		Hash:         r.Hash,
		IngestedAt:   timestamppb.New(r.IngestedAt),
		CredentialId: r.CredentialID,
	}
}

// ---- pg helpers ----

func pgUUID(s string) pgtype.UUID {
	u, err := uuid.Parse(s)
	if err != nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: u, Valid: true}
}

func ptrStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
