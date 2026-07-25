# Slice 484 — Framework-versioning capability decisions log

Slice type: JUDGMENT. The "superseded" status mapping, the immutability
enforcement mechanism, the migration-suggest heuristic, the deprecated/legacy
read policy, the write mechanism, and the CLI-vs-endpoint delivery are
subjective implementation calls bounded by ADR 0019. Per the JUDGMENT workflow,
Claude made the calls using best-reasoned, pattern-matched judgment against the
recorded ADR, recorded them here, and the slice ships when CI is green.
Auditors tune these post-deployment from the "Revisit once in use" list.

- detection_tier_actual: integration
- detection_tier_target: integration

Three defects surfaced DURING the build, all caught by the live-Postgres
integration tests (the correct tier), none escaping to a later tier:

1. A test-seed bug — the synthetic anchor's framework_version was seeded
   `current` under the SAME framework as the test versions, so the framework
   had two `current` versions and `GetCurrentFrameworkVersion` (a `:one`) was
   ambiguous; the promote demote-step missed. Fixed by seeding the anchor under
   a separate framework (the at-most-one-current invariant the store relies on).
2. The immutability AC-8 test asserted the trigger via the `atlas_app` pool,
   which has no UPDATE grant on `framework_requirements` at all — it failed with
   permission-denied BEFORE the trigger fired. This surfaced the
   defense-in-depth shape (grant-blocked for the app role; trigger-blocked for
   the loader role) and the test now asserts both arms.
3. The slice-007 invariant-#1 FK guard
   (`TestImport_NoDirectRequirementToRequirementTableExists`) tripped on the new
   `framework_version_migrations` catalog table's two FKs into
   `framework_requirements`. This was a guard false-positive (the table is an
   intra-framework carryover queue, not a cross-framework crosswalk); the guard
   was refined to exclude it by name with a constitutional justification (D5).

The first DELETE-trigger design (BEFORE UPDATE OR DELETE) was also caught and
corrected at this tier: the integration cleanup proved a DELETE guard makes a
frozen version permanently undeletable by ANY role (it fires on CASCADE and for
the superuser), breaking catalog GC — so the trigger was narrowed to UPDATE only
(D2), which is what §3.3 / threat-model T actually demand.

---

## Decisions made

### D1 — "Superseded" status maps to the existing `legacy` enum value

**Context:** ADR 0019 §1 says the prior version becomes `superseded`, "reusing
the existing enum value", and asserts the enum already carries
`current`/`deprecated`/`superseded`.

**Reality:** the `framework_version_status` enum on `main` is
`{ current, legacy, withdrawn }` — there is no `superseded` or `deprecated`
value. (The ADR's premise was factually off on the literal value names.)

**Options considered:**

1. **Add `superseded` to the enum** via `ALTER TYPE ... ADD VALUE` to match the
   ADR's literal word. Rejected: it contradicts the ADR's own core premise ("no
   schema rebuild; the enum already has the needed status values"), and Postgres
   `ADD VALUE` is awkward to reverse cleanly in a down migration.
2. **Reuse the existing `legacy` value** as the ADR's "superseded" semantic
   (chosen). `legacy` already means "an older version replaced by a current one,
   still valid for historical reference" — exactly the ADR's "replaced-but-valid"
   definition — and the `soc2import` loader already demotes to `legacy`. The ADR's
   `deprecated` ("discouraged") semantic maps to the existing `withdrawn`.

**Chosen:** option 2. The implementation honors the ADR's _substance_
(replaced-but-valid status, readable-when-pinned, distinct from a
discouraged/withdrawn status) and its _premise_ (no enum change, no schema
rebuild) by reading the existing `legacy` value as "superseded". Recorded
prominently in the migration header + ADR Implementation notes so a reader who
greps for "superseded" finds the mapping.

**Confidence:** high. `legacy` is a precise semantic fit; no behavior the ADR
specifies is lost.

### D2 — Immutability mechanism: a DB trigger on UPDATE only

**Options considered:**

1. **Service-layer guard in the loader** — reject in-place edits in Go. Rejected
   as the sole mechanism: it only protects the one code path that goes through
   the loader; a direct SQL UPDATE (or a future second writer) bypasses it.
2. **DB trigger on UPDATE OR DELETE** — the first cut. Rejected after live
   testing: a DELETE guard fires on FK CASCADE and even for the superuser,
   making a frozen version permanently undeletable and breaking catalog GC +
   test teardown.
3. **DB trigger on UPDATE only** (chosen) — `trg_framework_requirement_immutability`
   rejects an in-place UPDATE of a requirement whose version is
   current/legacy/withdrawn. §3.3 / threat-model T forbid _editing_ a frozen
   version's requirements in place ("changes ship as a new version, not in-place
   edits") — that is an UPDATE; a DELETE (version teardown) is a legitimate
   lifecycle op, not an edit.

**Chosen:** option 3, layered with a grant backstop: `atlas_app` holds only
SELECT on `framework_requirements` (no UPDATE at all), so the app role is
grant-blocked and the trigger additionally stops the privileged loader role from
a content-changing in-place re-import. Defense in depth.

**Confidence:** high. The integration test proves the trigger rejects the loader
role's in-place edit AND the app role is grant-denied, with the row unchanged.

### D3 — Write mechanism: narrow column grants (not a privileged pool)

Chose `GRANT UPDATE(status) ON framework_versions` + `GRANT
UPDATE(latest_version_id) ON frameworks` to `atlas_app`, plus SELECT/INSERT on
the review queue + audit and a narrow UPDATE on the queue's decision columns —
mirroring slice 483's pattern. The store runs the lifecycle under the app role;
the legality is enforced in Go and the trust gate is the admin-role authz check.
Rejected routing the lifecycle write through a BYPASSRLS pool: these are
catalog-level edits, not a cross-tenant escape hatch. **Confidence:** high
(identical shape to the shipped slice-483 mechanism).

### D4 — Migration-suggest delivery: an admin endpoint

The issue allowed "CLI/admin job". Chose an admin endpoint
(`POST /v1/admin/framework-versions/migrations:suggest`) over a `cmd/`
subcommand so the whole capability (suggest + promote + approve/reject) shares
one admin-authz boundary and one audit path on the `/v1` surface. **Confidence:**
medium-high — a CLI would also be defensible for an offline batch op, but the
endpoint composes better with the existing admin surfaces and is testable
through the same harness.

### D5 — The review queue is an intra-framework table, not an invariant-#1 crosswalk

`framework_version_migrations` is catalog-level (no `tenant_id`) and carries two
FKs into `framework_requirements`. The slice-007 invariant-#1 guard treats any
catalog FK into `framework_requirements` (beyond `fw_to_scf_edges`) as a
forbidden framework-to-framework bridge. That is a false positive here: the
queue's rows are intra-framework carryovers between two adjacent versions of the
SAME framework (P0-484-4 / invariant #7), never a cross-framework
requirement→requirement mapping that bypasses SCF anchors. Refined the guard to
exclude `framework_version_migrations` by name (exactly as `fw_to_scf_edges` is
the one allowed referencer); a genuinely-new cross-framework bridge with any
other name is still caught. **Confidence:** high — the exclusion is narrow and
the guard's positive test (a rolled-back second crosswalk still trips it) is
untouched.

### D6 — Version-aware read policy as implemented

- A pinned read (`?framework_version=slug:version`) returns exactly that
  version's requirements, no cross-version bleed (the load-bearing P0-484-5
  property, proven by `TestAnchorRequirements_VersionPinNoBleed`).
- Absent a pin, the read defaults to each framework's CURRENT version — a new
  `ListRequirementsForAnchorCurrentVersions` query filters `fv.status =
'current'`. (The prior behavior returned ALL versions; AC-5 made
  current-only the honest default.) Because every framework on `main` has
  exactly one version today, current-only == all-versions for existing data, so
  no existing read changes.
- `legacy` ("superseded") versions are readable ONLY when explicitly pinned
  (ADR 0019 §4), never the default, never hidden.

**Synthetic-revision version string:** the integration proof loads a synthetic
adjacent SOC 2 revision with version string `2017-synthetic-rev` (and the
Playwright mock + the no-bleed test use the same string). It is clearly labeled
synthetic by the version string itself and by the framework name
("slice484 synthetic SOC2"); it is seeded as test/seed data only and never
pollutes the real `soc2:2017` catalog.

---

## Revisit once in use

- **Title-similarity recall.** The suggest heuristic is exact-code-match only
  (ADR 0019 §3, precision-first). Once operators have used it, if the
  added/removed flagged set is consistently a rename pair they reconcile by
  hand, add a title-similarity pass that proposes (not auto-applies) likely
  renames. Deferred enhancement; a spillover slice was filed.
- **Applying an approved carryover.** Approval today records human acceptance of
  the SUGGESTION; applying the carryover edges to the new version is the loader's
  job under a separate human-driven import (P0-484-1 — the platform suggests,
  the human approves; nothing auto-applies). If operators want a one-click
  "apply approved carryovers", design that as an explicit, audited apply step
  that still respects the immutability rule (the target version must be the
  not-yet-frozen new version).
- **A real `draft` status.** The immutability trigger already accommodates a
  future `draft` status (it freezes only current/legacy/withdrawn). If staged
  loads want a mutable pre-promotion state, add `draft` to the enum and load new
  versions as `draft` → promote to `current`.
- **`deprecated` vs `withdrawn` nuance.** This slice uses `withdrawn` as the
  ADR's "deprecated"/discouraged status. If the product later needs both
  "discouraged but supported" and "fully withdrawn", split the enum then.
