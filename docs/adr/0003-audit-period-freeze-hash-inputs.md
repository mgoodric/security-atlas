# ADR 0003 — Audit period freeze hash: content-only inputs

**Status:** Accepted · Honored (verified 2026-05-15 by slice 071 audit — slice 028 ships `frozen_hash` over content-only inputs; `frozen_at` lives alongside the hash, not inside it; canonical-JSON serialization with sorted keys is in place)

**Date:** 2026-05-13

**Resolves:** slice 028 AC-7 ("freezing the same content twice produces the same hash") against the design ambiguity in canvas §8.4 ("frozen state is hashed and signed; tampering is detectable").

**Implements through:** [`docs/issues/028-audit-period-freezing.md`](../issues/028-audit-period-freezing.md)

---

## Context

Slice 028 freezes an `AuditPeriod` and persists a tamper-evident hash of the frozen state in `audit_periods.frozen_hash`. Canvas §8.4 commits to "frozen state is hashed and signed; tampering is detectable" but does not specify which fields feed the hash.

Two natural readings of AC-7 ("freezing the same content twice produces the same hash"):

1. **Wall-clock reading.** "Same content" means same evidence universe + same control set. Re-freezing the same row (after directly rewriting `status='open'`, which only a privileged operator or test harness can do) at a different wall-clock instant should still yield identical hash bytes if no underlying ledger record changed.
2. **Event reading.** "Same content" means the same freeze event — same wall-clock moment too — and AC-7 is just asserting determinism for a single freeze call repeated against an unchanged inputs.

These readings have a single concrete consequence: **does `frozen_at` belong in the hash input set or not?**

If `frozen_at` is in the input set, reading #1 fails: two freezes seconds apart of the same content produce different hashes.

If `frozen_at` is out of the input set, reading #1 holds: re-running the hash function on a row whose ledger universe has not changed produces identical bytes, regardless of which `frozen_at` is recorded alongside.

A third reality: a future signing step (cosign on OSCAL export bundles per canvas tech stack §9 — "cosign signing of audit-export bundles") will sign over the tuple `(frozen_at, frozen_hash)` at export time. The signature commits to the wall-clock moment of the freeze; the hash commits to the _content_ of what was frozen. Putting `frozen_at` inside the hash conflates two separable claims.

## Decision

The freeze hash is computed over **content-only inputs**, in this canonical order:

```
sha256(canonical_json({
  "audit_period_id":       <UUID>,
  "period_start":          <ISO-8601 date>,
  "period_end":            <ISO-8601 date>,
  "framework_version_id":  <UUID>,
  "evidence_record_ids":   <sorted UUID array, where observed_at <= frozen_at>,
  "control_ids":           <sorted UUID array, in period scope>
}))
```

Canonical JSON: keys in the literal order above (NOT alphabetical), values UTF-8, no whitespace, arrays sorted by string comparison of the UUID forms.

`frozen_at` is **NOT** an input to the hash. It is persisted alongside the hash on the same row and is part of the audit-log entry, so the wall-clock moment is recoverable from any frozen period — it just isn't mixed into the content commitment.

`frozen_by` is **NOT** an input either, by the same argument: who froze it is metadata about the freeze event, not content of what was frozen.

## Consequences

- AC-7 holds under the natural reading: rolling a row back to `status='open'` and re-freezing produces identical hash bytes if and only if the underlying evidence universe and the period's scope have not changed. Tampering with the ledger after freeze is therefore detectable by recomputing the hash and comparing.

- A future cosign signing step over `(frozen_at, frozen_hash, signer_identity)` can commit to both content and event-time without redundancy.

- The hash does NOT prove "this hash was produced at time T." If that claim becomes important (e.g., transparency log entry per future tier-3 evidence integrity), the signing step provides it.

- The canonical-JSON shape is intentionally narrow — six keys, three primitive types (UUID, date, array of UUID) — so a Python or TypeScript reimplementation in a future verifier tool has zero ambiguity.

- We do NOT include `tenant_id` in the hash because the period id is already tenant-unique (`(tenant_id, id)` composite uniqueness) and the verifier always operates inside one tenant's RLS context.

## Alternatives considered

1. **Include `frozen_at`.** Rejected: breaks AC-7 under the wall-clock reading, which is the practitioner-natural reading ("audit said the dashboard hasn't changed since freeze — prove it"). Wall-clock differences between two equivalent freezes are bookkeeping, not content.

2. **Include the full payload (or payload hash) of each evidence record.** Rejected: the evidence*records table already stores a per-record `hash` (slice 002 + slice 003). The freeze hash binds the \_set* of records (by id); the records' hashes bind the _content_ of each record. Composing the two at verification time gives full chain detection without making the freeze hash quadratic in evidence volume.

3. **Use a Merkle tree over evidence record hashes.** Rejected for v1: the linear sha256-over-sorted-ids is sufficient for the immediate threat (set-membership tampering), and Merkle adds verifier complexity. Tier-3 transparency-log work can graduate to Merkle when warranted.

4. **Hash the literal SQL result rows.** Rejected: row order, NULL serialization, and timestamp precision all leak engine details into the hash. Canonical-JSON over an explicit input contract is portable.
