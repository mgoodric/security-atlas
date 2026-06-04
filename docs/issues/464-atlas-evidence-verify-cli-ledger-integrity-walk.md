# 464 — `atlas evidence verify` CLI: ledger-wide integrity walk (+ SELF_HOSTING migrate-up phrasing drift)

**Cluster:** CLI / Evidence integrity
**Estimate:** M
**Type:** JUDGMENT
**Status:** `ready`

## Surfaced during slice 432

While building the backup/restore runbook (slice 432), grounding the
restore-drill integrity assertion against the shipped CLI surface
surfaced a documentation-vs-reality gap: **`atlas evidence verify` does
not exist.**

Three places reference it as though it ships:

- The slice-432 spec (AC-5 + notes: "`atlas evidence verify` + `atlas oscal verify`").
- `docs/SELF_HOSTING.md` "Backups" (old line: "a corrupted artifact is detectable by re-running `atlas evidence verify`" — rewritten by slice 432 to point at the published runbook, which does NOT claim the verb exists).
- `docs/governance/business-continuity.md` §6 Scenarios A/B/C "Verify" steps (`atlas-cli evidence verify --tenant=<self>` / `atlas-cli evidence verify`).

**What actually ships:** `cmd/atlas-cli/cmd_evidence.go` exposes only
`evidence push`. The per-record sha256 content hash **is** computed and
stored at ingest (`internal/evidence/ingest/ingest.go:172` — "canonjson
sha256 of the record") and is relied on at every ledger read (canvas
invariant #3, the append-only ledger). But there is **no operator-facing
command** to re-walk the whole ledger on demand and report records whose
stored hash no longer matches a recomputed canonical hash.

This is the integrity check the BCP/DR plan's restore scenarios and the
slice-432 restore drill both want. Slice 432 grounded its drill in the
shipping surface that DOES exist — `atlas oscal verify <bundle-dir>`,
which verifies a signed export bundle digest end-to-end — and added an
admonition to `docs-site/docs/backup-restore.md` that a ledger-wide
verify verb is not yet present.

## What to build

1. **`atlas evidence verify`** (`cmd/atlas-cli`): walk the evidence ledger
   for a tenant (or all tenants for a super-admin), recompute each
   record's canonical-JSON sha256, and report any record whose stored
   `hash` does not match — i.e. a silently-corrupted or tampered record.
   Role-correct: reads as `atlas_app` under RLS for a tenant-scoped walk;
   the cross-tenant walk uses the documented `atlas_service_account`
   `SET LOCAL ROLE` path, not a superuser connection.
2. **Wire the real command back into the docs** once it exists: replace
   the slice-432 "not yet present" admonition in `backup-restore.md` with
   the real command in the drill's assert step; the signed-OSCAL-export
   verify stays as the cryptographic end-to-end check alongside it.
3. **Correct the `SELF_HOSTING.md` migration-prose drift** (secondary
   item). `docs/SELF_HOSTING.md` references `docker compose run --rm atlas
atlas migrate up` and `ATLAS_MIGRATE_ON_START=true` — **neither exists
   in the shipped binary** (`cmd/atlas/main.go` handles only
   `--version`/`version`; `ATLAS_MIGRATE_ON_START` is absent from `cmd/`
   and `.env.example`). The real migration runner is the `atlas-bootstrap`
   one-shot. Slice 432's `upgrade.md` already routes around this with the
   real command; this slice corrects the `SELF_HOSTING.md` phrasing
   directly so the in-repo guide stops documenting a nonexistent
   subcommand. (Decide: either implement an `atlas migrate up` subcommand
   to match the docs, OR fix the docs to the bootstrap-one-shot reality —
   the docs-fix is the lower-cost path and is recommended unless an
   operator-facing migrate verb is independently wanted.)

## Acceptance criteria

- [ ] **AC-1.** `atlas evidence verify` exists, recomputes each record's
      canonical sha256, and reports mismatches (exit non-zero on any mismatch).
- [ ] **AC-2.** Tenant-scoped walk runs as `atlas_app` under RLS; the
      cross-tenant walk uses `atlas_service_account` `SET LOCAL ROLE`, never a
      superuser connection.
- [ ] **AC-3.** Integration test: a deliberately-corrupted record is
      reported; a clean ledger reports zero mismatches.
- [ ] **AC-4.** `docs-site/docs/backup-restore.md` drill assert step is
      updated to use the real command; the "not yet present" admonition is
      removed.
- [ ] **AC-5.** `docs/SELF_HOSTING.md` migration prose is corrected to the
      shipped mechanism (or the `atlas migrate up` subcommand is implemented to
      match — pick one and record it).
- [ ] **AC-6.** `docs/governance/business-continuity.md` §6 verify-step
      references resolve to the real command (coordinate with slice 373's owner
      per the BCP/DR plan's annual-review reconciliation, or note the deferral).

## Notes

- Parent: **slice 432** (backup/restore + upgrade runbooks). 432 grounded
  its drill in the shipping `atlas oscal verify` surface and filed this for
  the missing ledger-wide verb.
- The canonical-hash derivation must match the ingest path
  (`internal/evidence/ingest/ingest.go` — canonjson then sha256) exactly,
  or a correct record will false-positive. Reuse the ingest hashing helper
  rather than reimplementing the canonicalization.
- BCP/DR plan coordination (AC-6) crosses into a slice-373-owned governance
  doc; do not edit it unilaterally — either coordinate or note for the
  annual review per the plan's §11 reconciliation clause.
