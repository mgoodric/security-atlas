// Package oscal owns the slice-030 OSCAL export pipeline: it aggregates a
// FROZEN AuditPeriod's data from across the platform, hands it to the
// Python oscal-bridge for canonical OSCAL JSON v1.1.x serialization,
// round-trip-validates every document, assembles a signed export bundle,
// and writes it to disk.
//
// Constitutional invariants:
//
//   - Invariant 8 (OSCAL is the wire format): the bundle members are
//     canonical OSCAL JSON v1.1.x — SSP, Assessment Plan, Assessment
//     Results, POA&M. Serialization is delegated to compliance-trestle
//     via the Python bridge; this package never hand-rolls OSCAL.
//   - Invariant 10 (audit-period freezing): Export refuses to run unless
//     the AuditPeriod's status is 'frozen'. Every horizon-bounded read
//     uses the period's frozen_at as the upper bound, so live evidence
//     written after the freeze never leaks into the auditor's bundle.
//   - Product-runtime AI-assist boundary (CLAUDE.md, hard): the export
//     pipeline imports NO inference client. SSP control-implementation
//     narratives are the human-authored control-bundle descriptions,
//     carried verbatim. There is no code path here that generates
//     audit-binding narrative text.
//
// oscal.go holds the package's public types and the top-level Export
// orchestration. aggregate.go does the database reads; bridge.go is the
// gRPC client; sign.go is the bundle signer; bundle.go assembles and
// writes the bundle.
package oscal

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrPeriodNotFrozen is returned by Export (and Aggregate) when the
// target AuditPeriod has not been frozen. This is the enforcement point
// for constitutional invariant 10 and the P0 anti-criterion "does NOT
// export from a non-frozen period". Handlers map it to HTTP 409.
var ErrPeriodNotFrozen = errors.New("oscal: audit period is not frozen; export requires a frozen period")

// ErrPeriodNotFound is returned when the period id does not resolve under
// the active tenant.
var ErrPeriodNotFound = errors.New("oscal: audit period not found")

// ErrBridgeUnavailable wraps any failure to reach the Python oscal-bridge
// gRPC service. Handlers map it to HTTP 503.
var ErrBridgeUnavailable = errors.New("oscal: oscal-bridge service unavailable")

// ErrRoundTripFailed is returned when a serialized OSCAL document fails
// the compliance-trestle round-trip validation. The export aborts — a
// bundle is never finalized with an invalid member (AC-6/AC-7 + P0
// anti-criterion "does NOT skip compliance-trestle round-trip").
var ErrRoundTripFailed = errors.New("oscal: compliance-trestle round-trip validation failed")

// ErrSigningFailed is returned when the bundle could not be signed. The
// export aborts WITHOUT writing an unsigned bundle (AC-5 + P0
// anti-criterion "does NOT skip cosign signing of export bundle").
var ErrSigningFailed = errors.New("oscal: bundle signing failed")

// OSCALVersion is the OSCAL specification version security-atlas commits
// to (canvas §3.4). The Python bridge stamps every emitted document with
// this value; the Go side records it in the bundle manifest.
const OSCALVersion = "1.1.2"

// ExportInput parameterizes a single export run.
type ExportInput struct {
	// AuditPeriodID is the frozen period to export. Required.
	AuditPeriodID uuid.UUID
	// OrganizationName / SystemName / SystemDescription populate the SSP
	// org profile and system-characteristics. The platform does not yet
	// have a dedicated org-profile table (that is a later slice), so the
	// caller supplies these; the CLI takes them as flags, the HTTP
	// handler from the request body. Empty values fall back to safe
	// defaults inside the bridge.
	OrganizationName  string
	SystemName        string
	SystemDescription string
	// RequestedBy is the credential / user id that triggered the export,
	// recorded in the bundle manifest for the audit trail.
	RequestedBy string
}

// BundleMember is one OSCAL document inside the export bundle.
type BundleMember struct {
	// Filename is the bundle-relative path, e.g. "ssp.json".
	Filename string
	// ModelType is the OSCAL model: system-security-plan,
	// assessment-plan, assessment-results, or
	// plan-of-action-and-milestones.
	ModelType string
	// JSON is the canonical OSCAL JSON v1.1.x document bytes.
	JSON []byte
	// SHA256 is the lowercase-hex content hash of JSON.
	SHA256 string
}

// Bundle is the assembled, signed export bundle held in memory before it
// is written to disk. WriteBundle persists it.
type Bundle struct {
	// AuditPeriodID is the frozen period this bundle was generated from.
	AuditPeriodID uuid.UUID
	// FrozenAt is the period's freeze horizon, RFC-3339.
	FrozenAt string
	// OSCALVersion is the pinned OSCAL spec version (see OSCALVersion).
	OSCALVersion string
	// GeneratedAt is the wall-clock instant the export ran, RFC-3339.
	GeneratedAt string
	// RequestedBy is the credential / user id that triggered the export.
	RequestedBy string
	// Members are the four OSCAL documents (SSP, AP, AR, POA&M).
	Members []BundleMember
	// Signature is the detached signature over the bundle digest. A
	// Bundle is NEVER returned from Export with a zero Signature — see
	// ErrSigningFailed.
	Signature Signature
}

// Exporter wires the database pool and the bridge client. Construct one
// per process; it is safe for concurrent use.
type Exporter struct {
	pool   *pgxpool.Pool
	bridge BridgeClient
	signer *Signer
}

// NewExporter wires an Exporter. The pool is held but not owned (callers
// close it). The bridge client is the gRPC connection to the Python
// oscal-bridge service. The signer holds the ed25519 key used to sign
// export bundles.
func NewExporter(pool *pgxpool.Pool, bridge BridgeClient, signer *Signer) *Exporter {
	return &Exporter{pool: pool, bridge: bridge, signer: signer}
}

// Export runs the full pipeline for one frozen AuditPeriod:
//
//  1. Aggregate the period's data from the database (refuses if not
//     frozen — invariant 10).
//  2. Serialize SSP, AP, AR, POA&M via the Python bridge (invariant 8).
//  3. Round-trip-validate every document via compliance-trestle; abort
//     on any failure (AC-6/AC-7).
//  4. Assemble the bundle and sign its digest; abort if signing fails —
//     no unsigned bundle is ever returned (AC-5).
//
// The returned Bundle is in-memory; the caller persists it with
// WriteBundle. ctx must carry a tenancy value.
func (e *Exporter) Export(ctx context.Context, in ExportInput) (*Bundle, error) {
	if in.AuditPeriodID == uuid.Nil {
		return nil, fmt.Errorf("oscal: audit_period_id is required")
	}

	agg, err := e.Aggregate(ctx, in)
	if err != nil {
		// Aggregate already returns ErrPeriodNotFrozen / ErrPeriodNotFound
		// unwrapped; pass them through verbatim.
		return nil, err
	}
	return e.exportFromAggregate(ctx, agg, in.RequestedBy)
}

// exportFromAggregate runs the post-aggregation pipeline: serialize ->
// round-trip-validate -> assemble -> sign. It is split out from Export so
// the bridge/round-trip/signing behavior can be unit-tested with a fake
// bridge and a hand-built aggregate, without a database.
func (e *Exporter) exportFromAggregate(ctx context.Context, agg *aggregate, requestedBy string) (*Bundle, error) {
	members, err := e.serializeAll(ctx, agg)
	if err != nil {
		return nil, err
	}

	// Round-trip-validate EVERY member before the bundle is finalized.
	// A single failure aborts the export — invariant 8 + AC-6/AC-7.
	for _, m := range members {
		valid, vErrs, err := e.bridge.RoundTripValidate(ctx, m.ModelType, m.JSON)
		if err != nil {
			return nil, fmt.Errorf("%w: round-trip RPC for %s: %v", ErrBridgeUnavailable, m.Filename, err)
		}
		if !valid {
			return nil, fmt.Errorf("%w: %s: %v", ErrRoundTripFailed, m.Filename, vErrs)
		}
	}

	bundle := assembleBundle(agg, members, requestedBy)

	// Sign the bundle digest. Signing failure aborts the export — the
	// caller never receives an unsigned Bundle (AC-5, P0 anti-criterion).
	sig, err := e.signer.SignBundle(bundle)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSigningFailed, err)
	}
	bundle.Signature = sig

	return bundle, nil
}

// serializeAll calls the bridge to produce all four OSCAL documents.
func (e *Exporter) serializeAll(ctx context.Context, agg *aggregate) ([]BundleMember, error) {
	sspJSON, err := e.bridge.SerializeSSP(ctx, agg.sspInput())
	if err != nil {
		return nil, fmt.Errorf("%w: SerializeSSP: %v", ErrBridgeUnavailable, err)
	}
	apJSON, arJSON, err := e.bridge.SerializeAssessment(ctx, agg.assessmentInput())
	if err != nil {
		return nil, fmt.Errorf("%w: SerializeAssessment: %v", ErrBridgeUnavailable, err)
	}
	poamJSON, err := e.bridge.SerializePOAM(ctx, agg.poamInput())
	if err != nil {
		return nil, fmt.Errorf("%w: SerializePOAM: %v", ErrBridgeUnavailable, err)
	}

	return []BundleMember{
		newMember("ssp.json", "system-security-plan", sspJSON),
		newMember("assessment-plan.json", "assessment-plan", apJSON),
		newMember("assessment-results.json", "assessment-results", arJSON),
		newMember("poam.json", "plan-of-action-and-milestones", poamJSON),
	}, nil
}
