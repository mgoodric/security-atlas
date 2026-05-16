# OSCAL SSP/AP/AR/POA&M Export — From Frozen Period to Signed Bundle

_2026-05-16T06:19:09Z by Showboat 0.6.1_

<!-- showboat-id: b68c9652-d16c-45ef-a886-80d059c0a99e -->

> **Walkthrough kind:** this is a PAI Walkthrough skill document (slice 070 — showboat-generated). It is distinct from slice 027’s audit walkthrough (`internal/audit/walkthrough`), which records auditor evidence capture against controls. The two concepts share a word and nothing else.

## Overview

When an audit period is frozen, the platform can hand off the entire audit-binding view as a canonical OSCAL JSON v1.1.x bundle: System Security Plan (SSP), Assessment Plan (AP), Assessment Results (AR), Plan of Action & Milestones (POA&M), plus a signed manifest. The bundle is what an auditor consumes.

Two architectural commitments shape this surface:

- **OSCAL is the wire format, not the daily data model** (constitutional invariant 8). The Go platform holds richer aggregates internally; serialization to OSCAL happens only on export, delegated to IBM `compliance-trestle` via a Python sidecar.
- **The product never publishes audit-binding artifacts without one-click human approval** (AI-assist boundary in `CLAUDE.md`). The export pipeline imports no inference client; SSP narrative text comes from the human-authored control bundles, carried verbatim.

This walkthrough traces a frozen period through aggregation, the gRPC bridge to compliance-trestle, round-trip validation, cosign-compatible signing, and bundle writing. Captured against the slice-037 docker-compose bundle, seeded by `fixtures/walkthroughs/00-seed.sql` + `audit-period.sql` + `oscal-export.sql` (which freezes the period).

## 1. The Frozen Period — Pre-condition

The export refuses to run against a non-frozen period (`ErrPeriodNotFrozen` → HTTP 409). The fixture freezes the SOC2 Q1 2026 period:

```bash
docker exec security-atlas-pg-030 psql -U atlas_app -d security_atlas -c "BEGIN; SET LOCAL app.current_tenant = '00000000-0000-0000-0000-00000000d3a0'; SELECT name, status, frozen_at, encode(frozen_hash, 'hex') AS frozen_hash_hex FROM audit_periods WHERE id = '55555555-5555-5555-5555-555555550001'; ROLLBACK;"
```

```output
BEGIN
SET
     name     | status |       frozen_at        |                         frozen_hash_hex
--------------+--------+------------------------+------------------------------------------------------------------
 SOC2 Q1 2026 | frozen | 2026-04-15 12:00:00+00 | a17ed3a0a17ed3a0a17ed3a0a17ed3a0a17ed3a0a17ed3a0a17ed3a0a17ed3a0
(1 row)

ROLLBACK
```

Status `frozen`, `frozen_at` stamped, `frozen_hash` non-NULL. Now the export gate opens.

## 2. The Refusal Path — Non-Frozen Periods

The Go side enforces the constraint declaratively. Looking at the error type + handler mapping:

```bash
cd /Users/gmoney/Development/security-atlas-070 && grep -B1 -A 4 "ErrPeriodNotFrozen" internal/oscal/oscal.go internal/oscal/aggregate.go 2>&1 | head -25
```

```output
internal/oscal/oscal.go-
internal/oscal/oscal.go:// ErrPeriodNotFrozen is returned by Export (and Aggregate) when the
internal/oscal/oscal.go-// target AuditPeriod has not been frozen. This is the enforcement point
internal/oscal/oscal.go-// for constitutional invariant 10 and the P0 anti-criterion "does NOT
internal/oscal/oscal.go-// export from a non-frozen period". Handlers map it to HTTP 409.
internal/oscal/oscal.go:var ErrPeriodNotFrozen = errors.New("oscal: audit period is not frozen; export requires a frozen period")
internal/oscal/oscal.go-
internal/oscal/oscal.go-// ErrPeriodNotFound is returned when the period id does not resolve under
internal/oscal/oscal.go-// the active tenant.
internal/oscal/oscal.go-var ErrPeriodNotFound = errors.New("oscal: audit period not found")
--
internal/oscal/oscal.go-	if err != nil {
internal/oscal/oscal.go:		// Aggregate already returns ErrPeriodNotFrozen / ErrPeriodNotFound
internal/oscal/oscal.go-		// unwrapped; pass them through verbatim.
internal/oscal/oscal.go-		return nil, err
internal/oscal/oscal.go-	}
internal/oscal/oscal.go-	return e.exportFromAggregate(ctx, agg, in.RequestedBy)
--
internal/oscal/aggregate.go-// the enforcement point for constitutional invariant 10: if the period
internal/oscal/aggregate.go:// is not frozen it returns ErrPeriodNotFrozen and reads nothing further.
internal/oscal/aggregate.go-//
internal/oscal/aggregate.go-// Every read runs inside one transaction under the tenant RLS context
internal/oscal/aggregate.go-// (mirrors the slice-028 period.Store.inTx pattern). ctx must carry a
internal/oscal/aggregate.go-// tenancy value.
--
```

`Aggregate` reads the period first; if `status != frozen` it returns `ErrPeriodNotFrozen` and reads nothing further. `Export` calls `Aggregate` and would short-circuit on the same error.

## 3. The Aggregator — Reading the Frozen View

`internal/oscal/aggregate.go::Aggregate` pulls together everything that goes into the bundle:

```bash
cd /Users/gmoney/Development/security-atlas-070 && sed -n "45,80p" internal/oscal/aggregate.go
```

```output

// Aggregate reads a frozen AuditPeriod's data from the database. It is
// the enforcement point for constitutional invariant 10: if the period
// is not frozen it returns ErrPeriodNotFrozen and reads nothing further.
//
// Every read runs inside one transaction under the tenant RLS context
// (mirrors the slice-028 period.Store.inTx pattern). ctx must carry a
// tenancy value.
func (e *Exporter) Aggregate(ctx context.Context, in ExportInput) (*aggregate, error) {
	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return nil, err
	}
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		return nil, fmt.Errorf("oscal: parse tenant id: %w", err)
	}

	tx, err := e.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("oscal: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return nil, err
	}
	q := dbx.New(tx)

	agg := &aggregate{
		in:           in,
		controlOwner: map[uuid.UUID]string{},
		controlTitle: map[uuid.UUID]string{},
	}

	// 1. Resolve the period and assert it is frozen. This MUST be the
	//    first check — invariant 10 + P0 anti-criterion.
```

Note three load-bearing details:

1. **Tenant context from the request** — RLS still applies. An auditor cross-tenant probe goes nowhere.
2. **One transaction for all reads** — `Aggregate` opens one transaction and reads every dataset (period, scope cells, control implementations, populations, walkthroughs, notes, failing evaluations). Read-skew impossible.
3. **The frozen check is the first read** — short-circuits the entire pipeline before any other DB work happens.

## 4. The Bridge — Why a Python Sidecar

OSCAL JSON v1.1.x is a sprawling spec with subtle field-name and conditional-requirement quirks. IBM `compliance-trestle` is the reference Python library. Rather than re-implement those rules in Go (and re-implement them wrongly), the platform shells out via gRPC to a thin Python service. The bridge has no business logic, no auth, no LLM. It is a serializer.

The Go-side client interface:

```bash
cd /Users/gmoney/Development/security-atlas-070 && sed -n "1,40p" internal/oscal/bridge.go
```

```output
package oscal

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	oscalv1 "github.com/mgoodric/security-atlas/gen/proto/oscal/v1"
)

// BridgeClient is the Go-side interface to the Python oscal-bridge gRPC
// service. It is an interface so tests can substitute a fake without a
// running Python process — the integration test wires the real client
// against a spawned bridge; the unit tests use a stub.
type BridgeClient interface {
	// SerializeSSP maps the SSP input to canonical OSCAL JSON v1.1.x.
	SerializeSSP(ctx context.Context, in *oscalv1.SspInput) ([]byte, error)
	// SerializeAssessment maps the assessment input to (AP JSON, AR JSON).
	SerializeAssessment(ctx context.Context, in *oscalv1.AssessmentInput) (apJSON, arJSON []byte, err error)
	// SerializePOAM maps the POA&M input to canonical OSCAL JSON v1.1.x.
	SerializePOAM(ctx context.Context, in *oscalv1.PoamInput) ([]byte, error)
	// RoundTripValidate parses an OSCAL document back through
	// compliance-trestle, returning whether it is structurally valid.
	RoundTripValidate(ctx context.Context, modelType string, oscalJSON []byte) (valid bool, errs []string, err error)
	// Close releases the underlying gRPC connection.
	Close() error
}

// grpcBridge is the production BridgeClient: a thin wrapper over the
// generated gRPC stub.
type grpcBridge struct {
	conn   *grpc.ClientConn
	client oscalv1.OscalBridgeServiceClient
}

// DialBridge connects to the Python oscal-bridge service at addr (e.g.
// "127.0.0.1:50070"). The connection is insecure (no TLS): the bridge is
```

`BridgeClient` is an interface so tests can substitute a fake. The integration test (`internal/oscal/integration_test.go`) wires the REAL Python bridge — spawning the process, dialing, exporting, and asserting that each member round-trip-validates. Unit tests use stubs.

## 5. Round-Trip Validation — The P0 Anti-Criterion

Every serialized document is parsed back through compliance-trestle to confirm it is structurally valid OSCAL. A failure aborts the export with `ErrRoundTripFailed` BEFORE the bundle is finalized. The slice 030 P0 anti-criterion is "does NOT skip compliance-trestle round-trip" — the test surface backs this:

```bash
cd /Users/gmoney/Development/security-atlas-070 && grep -n "RoundTripFailed\|ErrRoundTripFailed\|TestExport.*RoundTrip" internal/oscal/*.go 2>&1 | head -10
```

```output
internal/oscal/export_test.go:113:func TestExportFromAggregateAbortsOnRoundTripFailure(t *testing.T) {
internal/oscal/export_test.go:120:	if !errors.Is(err, ErrRoundTripFailed) {
internal/oscal/export_test.go:121:		t.Fatalf("expected ErrRoundTripFailed, got %v", err)
internal/oscal/export_test.go:136:func TestExportFromAggregateAbortsOnRoundTripRPCError(t *testing.T) {
internal/oscal/oscal.go:52:// ErrRoundTripFailed is returned when a serialized OSCAL document fails
internal/oscal/oscal.go:56:var ErrRoundTripFailed = errors.New("oscal: compliance-trestle round-trip validation failed")
internal/oscal/oscal.go:181:			return nil, fmt.Errorf("%w: %s: %v", ErrRoundTripFailed, m.Filename, vErrs)
```

## 6. Signing — Detached cosign-Compatible Signature

A finalized bundle has five members: the four OSCAL documents + a manifest. The manifest lists every member with its sha256, and the manifest itself is signed with an ed25519 private key in a cosign-compatible format. `internal/oscal/sign.go`:

```bash
cd /Users/gmoney/Development/security-atlas-070 && grep -n "func.*sign\|func.*Sign\|ed25519" internal/oscal/sign.go 2>&1 | head -10
```

```output
4:	"crypto/ed25519"
22:// uses an in-process ed25519 detached signature over the bundle's
30:	// Algorithm identifies the signing scheme. Always "ed25519" in v1.
32:	// PublicKey is the lowercase-hex ed25519 public key. A verifier uses
38:	// Signature is the lowercase-hex ed25519 signature over Digest's raw
44:var ErrNoSigningKey = errors.New("oscal: signer requires an ed25519 private key")
46:// Signer holds the ed25519 key material used to sign export bundles.
48:	priv ed25519.PrivateKey
49:	pub  ed25519.PublicKey
52:// NewSigner wires a Signer from an existing ed25519 private key. The key
```

The signer is held in-process; the key material is loaded from an operator-supplied PEM file. Signing failure (`ErrSigningFailed`) aborts the export WITHOUT writing the bundle — P0 anti-criterion "does NOT skip cosign signing".

## 7. The Bundle on Disk

`Bundle.WriteBundle(dir)` writes exactly five files. For demonstration, look at the canonical Manifest type, which any verifier reads first:

```bash
cd /Users/gmoney/Development/security-atlas-070 && sed -n "10,55p" internal/oscal/bundle.go
```

```output
	"time"
)

// Manifest is the bundle's manifest.json: it lists every member, records
// the export provenance, and carries the bundle signature. An auditor
// reading the bundle starts here.
type Manifest struct {
	// SchemaVersion identifies the manifest shape. Bumped on any
	// breaking change to this struct.
	SchemaVersion string `json:"schema_version"`
	// AuditPeriodID is the frozen period the bundle was generated from.
	AuditPeriodID string `json:"audit_period_id"`
	// FrozenAt is the period's freeze horizon, RFC-3339. Every member
	// was generated from data at or before this instant (invariant 10).
	FrozenAt string `json:"frozen_at"`
	// OSCALVersion is the OSCAL spec version every member conforms to.
	OSCALVersion string `json:"oscal_version"`
	// GeneratedAt is the wall-clock instant the export ran, RFC-3339.
	GeneratedAt string `json:"generated_at"`
	// RequestedBy is the credential / user id that triggered the export.
	RequestedBy string `json:"requested_by"`
	// Members lists each OSCAL document in the bundle with its content
	// hash.
	Members []ManifestMember `json:"members"`
	// Signature is the detached signature over the bundle digest. It is
	// ALWAYS present in a written manifest — Exporter.Export aborts
	// before WriteBundle if signing failed (AC-5, P0 anti-criterion).
	Signature Signature `json:"signature"`
}

// ManifestMember is one OSCAL document's manifest entry.
type ManifestMember struct {
	Filename  string `json:"filename"`
	ModelType string `json:"model_type"`
	SHA256    string `json:"sha256"`
	SizeBytes int    `json:"size_bytes"`
}

// ManifestFilename is the fixed name of the manifest inside the bundle.
const ManifestFilename = "manifest.json"

// SchemaVersion is the current manifest schema version.
const SchemaVersion = "oscal-export-bundle/v1"

// newMember builds a BundleMember, computing the content hash.
func newMember(filename, modelType string, jsonBytes []byte) BundleMember {
```

The manifest carries the period id, the freeze horizon (`frozen_at`), the OSCAL version, the request actor, and one entry per OSCAL document (filename + model type + sha256). The signature is over the bundle digest and is **always present** in a written manifest — `Export` aborts before `WriteBundle` if signing failed.

## 8. The CLI Surface

The end-user command for triggering an export from a terminal is the atlas-cli sub-command. Its help text spells out every safeguard:

```bash
/tmp/atlas-070-cli oscal-export --help 2>&1 | head -30
```

```output
Generates the OSCAL audit-handoff bundle for a FROZEN AuditPeriod:

  - system-security-plan.json   (SSP)
  - assessment-plan.json        (AP)
  - assessment-results.json     (AR)
  - poam.json                   (POA&M)
  - manifest.json               (member list + provenance + signature)

The bundle is round-trip validated through IBM compliance-trestle (via the
Python oscal-bridge sidecar) and signed before it is written. The export
REFUSES to run against a period that has not been frozen — the auditor's
view must draw only from evidence at or before the freeze horizon
(constitutional invariant 10).

Requires a running oscal-bridge (see oscal-bridge/README.md):

  python -m atlas_oscal_bridge.server --address 127.0.0.1:50070

Usage:
  security-atlas-cli oscal-export [flags]

Flags:
      --bridge-addr string          oscal-bridge gRPC address (default "127.0.0.1:50070")
      --dsn string                  Postgres DSN (atlas_app role); env DATABASE_URL_APP
  -h, --help                        help for oscal-export
      --org-name string             organization name for the SSP org profile
      --out string                  output directory for the bundle (required)
      --period-id string            frozen audit period UUID to export (required)
      --requested-by string         id recorded in the bundle manifest as the requester (default "atlas-cli")
      --system-description string   system description / authorization-boundary summary
```

## 9. Putting It All Together

| Stage        | Code path                                                                                  | Failure mode                               |
| ------------ | ------------------------------------------------------------------------------------------ | ------------------------------------------ |
| Frozen check | `aggregate.go::Aggregate` first read                                                       | `ErrPeriodNotFrozen` → HTTP 409            |
| Aggregate    | One transaction; period, scopes, controls, populations, walkthroughs, notes, failing evals | DB error → ErrAggregateRead                |
| Serialize    | gRPC to Python `oscal-bridge` → compliance-trestle                                         | bridge down → `ErrBridgeUnavailable` → 503 |
| Round-trip   | Each member parsed back via compliance-trestle                                             | `ErrRoundTripFailed` — abort, no bundle    |
| Sign         | ed25519 over the bundle digest                                                             | `ErrSigningFailed` — abort, no bundle      |
| Write        | Five files in `out/`                                                                       | Disk error — caller responsibility         |

The chain has three abort points before any file lands on disk. The frozen-period gate (constitutional invariant 10) and the round-trip validation (P0 anti-criterion) together guarantee that **every bundle on disk** describes a horizon-bounded view that compliance-trestle accepted and that is signed by the operator key. No half-state, no unverified outputs.

The AI-assist boundary is preserved structurally: `internal/oscal/` and `oscal-bridge/` together import no inference client. SSP narrative text is the operator-authored control-bundle description, carried verbatim. Slice 030 explicitly verifies this by absence.

### Where to read more

- **Canvas:** [`Plans/canvas/03-ucf.md`](https://github.com/mgoodric/security-atlas/blob/main/Plans/canvas/03-ucf.md) §3.4 — OSCAL is the wire format; [`Plans/canvas/08-audit-workflow.md`](https://github.com/mgoodric/security-atlas/blob/main/Plans/canvas/08-audit-workflow.md) §8.4 — freeze horizon
- **ADR:** [`docs/adr/0003-audit-period-freeze-hash.md`](https://github.com/mgoodric/security-atlas/blob/main/docs/adr/0003-audit-period-freeze-hash.md) — what the freeze hash commits
- **Slice docs:** [`docs/issues/030-oscal-ssp-poam-export.md`](https://github.com/mgoodric/security-atlas/blob/main/docs/issues/030-oscal-ssp-poam-export.md) (export pipeline), [`docs/issues/028-audit-period-freezing.md`](https://github.com/mgoodric/security-atlas/blob/main/docs/issues/028-audit-period-freezing.md) (the freeze gate this depends on)
- **Go packages:** [`internal/oscal/`](https://github.com/mgoodric/security-atlas/blob/main/internal/oscal/) — `Exporter.Export`, `Aggregate`, `Bundle`, `BridgeClient`
- **Python bridge:** [`oscal-bridge/`](https://github.com/mgoodric/security-atlas/blob/main/oscal-bridge/) — compliance-trestle wrapping
