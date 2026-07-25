# Slice 619 — accepted vendor claim → vendor-attested SSP control-implementation — decisions log

**Type:** JUDGMENT. The load-bearing call is _how_ an operator-accepted vendor
claim appears in the OSCAL SSP export and — critically — how it is structurally
prevented from ever reading as platform-verified evidence or contributing to
control-satisfaction/coverage. Parent: slice 589 (vendor-claim disposition),
which deferred this surface in its D2. Inherits the slice-512 P0-512-1 boundary
and canvas invariant #2.

**This slice does NOT touch the runtime AI-assist boundary.** There is no LLM
anywhere in this path. The "no fabricated coverage" rule it honors is the same
constitutional rule the AI-assist boundary names, applied here to a vendor's
_human-authored_ assertion rather than to model output.

**Detection-tier classification (slice 353 / Q-13):**

- detection_tier_actual: none — no bug surfaced during the slice. The boundary
  was designed in (separate proto field; no evaluation-result prop) and verified
  by the tests below, not discovered by a failure.
- detection_tier_target: integration — the load-bearing behaviours (the export
  writes nothing to `control_evaluations`; the accepted claim renders
  vendor-attested with provenance; rejected/needs_info claims are excluded) are
  pinned by `internal/oscal/vendor_attested_ssp_integration_test.go`. The
  proto-conversion separation is additionally pinned by a fast Go-side unit test
  (`internal/oscal/aggregate_proto_test.go`) and the OSCAL shape by a Python
  serializer unit test (`oscal-bridge/tests/test_serializer.py`).

---

## THE CONSTITUTIONAL BOUNDARY (the whole slice)

**How an accepted claim appears in the SSP:**

An operator-accepted vendor claim surfaces as an OSCAL `by-component`
control-implementation statement — the OSCAL-native construct for "this
_component_ implements this control this way". Concretely, for each accepted
claim the export emits:

1. A dedicated `SystemComponent` (type from the vendor's component-definition,
   e.g. `service`) in the SSP `system-implementation`, **distinct from the
   `this-system` component**, flagged `vendor-attested=true` /
   `operator-credited=true`. A vendor product is never the assessed system.
2. An `ImplementedRequirement` whose statement is a `by-components` entry
   pointing at that vendor component. The by-component `description` **leads with
   the `VENDOR-ATTESTED` honesty label** ("operator-credited vendor claim, NOT
   platform-verified evidence …"), carries the vendor's statement verbatim, and
   carries the accept-provenance as props: `disposition=accepted`,
   `accepted-by=<credential>`, `accepted-at=<RFC-3339>`, `claim-id=<uuid>`,
   plus `scf-id` when mapped.

**How it is prevented from satisfying a control / fabricating coverage:**

- **Separate proto field, never merged.** Accepted claims travel in a new
  `SspInput.vendor_attested_implementations` field, _never_ in
  `control_implementations`. The Go `sspInput()` conversion places them there
  and nowhere else; `aggregate_proto_test.go` asserts AC-2 (`AC-2` claim never
  appears in `ControlImplementations`).
- **No evaluation-result.** A vendor-attested `ImplementedRequirement` carries
  **no `evaluation-result` prop** — it is not a platform evaluation. The
  integration test asserts this on the emitted OSCAL.
- **Pure read; nothing is written.** The export adds one `SELECT`
  (`ListAcceptedVendorClaimsForExport`) over `imported_component_claims`. It
  writes nothing — no `control_evaluations`, no evidence ledger. The integration
  test asserts the `control_evaluations` **count is identical before and after**
  the export.
- **`is_vendor_claim=TRUE` and the slice-512 CHECK are untouched.** A claim is
  always a claim.

An SSP reader sees the statement attributed to the vendor's product, leading
with an unmistakable "NOT platform-verified" marker, with the operator's name
and the accept timestamp — i.e. "the vendor says X and the operator credited
it", never "the platform verified X".

---

## D1 — by-component statement, not a fabricated control-implementation

The OSCAL-correct way to say "a component asserts this implementation" is a
`by-component` statement under an `implemented-requirement`, attributed to a
`SystemComponent`. I chose this over (a) a top-level synthetic
control-implementation (would read as platform coverage) and (b) a free-text
remark on an existing platform control-implementation (would pollute the
platform control's statement and risk being read as platform evidence). The
by-component construct is the one OSCAL primitive that an auditor's tooling
already understands as "attributed to a third-party component", and it lets the
vendor component be a _separate_ system-component from `this-system`.

## D2 — claims are tenant-wide, not period-scoped

Unlike the SSP's control-implementations (which derive from the frozen period's
active controls), accepted vendor claims are read **tenant-wide**
(`claim_status = 'accepted'`), not bounded to the period's `frozen_at`. An
accepted vendor attestation is a standing fact about the operator's program (the
operator credited it once; it is not drawn from a frozen evidence population),
so the audit-period freezing horizon (invariant #10) does not apply to it the
way it applies to sampled evidence. This is a deliberate asymmetry. If a future
slice wants period-scoped vendor attestations (e.g. "credited during this
period"), the disposition audit table (`imported_component_claim_dispositions`,
slice 589) already carries `occurred_at` to support it — filed as a forward
note, not built here.

## D3 — unmapped (NULL scf_anchor) accepted claims are still exported

The spec (Key design calls) asks whether an accepted claim with an unmapped SCF
anchor is excluded until mapped (depends on #620). Decision: **export it
anyway**, using the claim's raw `control_id` as the OSCAL control token when no
`scf_id` is present (the `_oscal_token(vai.scf_id or vai.control_id)` fallback,
mirroring the platform control path's existing behaviour). Rationale: excluding
a credited claim purely for being unmapped would _hide_ an operator decision
from the SSP, which is the opposite of the transparency this surface exists for.
The claim is clearly vendor-attested regardless of mapping; mapping only
improves cross-framework correlation. Slice 620 (claim→SCF mapping) can later
enrich the `scf-id` prop, but it is not a gate on appearing in the SSP.

## D4 — no new route, no migration

This is a pure additive read inside the existing OSCAL export pipeline
(`internal/oscal`), wired through the existing `Export` → `Aggregate` →
`sspInput()` → bridge `SerializeSSP` path. No new HTTP route (so no
`openapi.yaml` change), no schema change (so no migration). The only generated
surfaces touched are: the new sqlc query (`just sqlc-generate`), the proto
message + field (`just proto-generate` for Go, `gen_proto.sh` at
`grpcio-tools==1.80.0` for Python — `GRPC_GENERATED_VERSION = '1.80.0'`
verified). New integration test rides the already-enrolled `internal/oscal`
package (enrolment audit OK).

## D5 — defensive skip of an un-attributable claim

In the Python serializer, a vendor claim is only rendered if its component was
seeded into `system-implementation` above. The two passes key off the same
`component_uuid` (falling back to title/claim_id), so this never fails in
practice — but if a claim somehow had no resolvable component, the serializer
**skips it** rather than emit a statement with no component attribution (which
could read as un-attributed, i.e. platform, evidence). Failing safe toward
"omit" rather than "emit un-attributed" is the boundary-preserving choice.

---

## Boundary tests (the proof)

- `internal/oscal/vendor_attested_ssp_integration_test.go`
  (`TestExport_AcceptedVendorClaim_VendorAttestedAndNoFabricatedCoverage`) —
  the load-bearing test: seeds one accepted + one rejected + one needs_info
  claim on a frozen period, exports, and asserts (1) `control_evaluations` count
  unchanged, (2) the accepted claim's vendor-attested label + statement +
  accept-provenance + claim-id appear, (3) the rejected/needs_info statements
  and ids do NOT appear, (4) the vendor-attested implemented-requirement carries
  no `evaluation-result`. Bridge-dependent content assertions self-skip without
  the trestle bridge (511/512/599 convention); the count-unchanged check is the
  boundary and runs Go-side regardless.
- `internal/oscal/aggregate_proto_test.go`
  (`TestSSPInput_AcceptedVendorClaims_AreSeparateAndNeverControlImplementations`,
  `TestSSPInput_NoAcceptedVendorClaims_ProducesEmptyVendorField`) — fast Go unit
  tests proving the proto-conversion separation (no DB, no bridge).
- `oscal-bridge/tests/test_serializer.py`
  (`test_serialize_ssp_renders_accepted_vendor_claim_as_by_component`,
  `test_serialize_ssp_excludes_rejected_and_needs_info_claims`) — prove the
  OSCAL shape: distinct vendor component, by-component statement, leading honesty
  label, accept-provenance props, no evaluation-result, trestle round-trip.
