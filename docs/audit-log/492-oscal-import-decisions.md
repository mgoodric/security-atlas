# Decisions log — slice 492: OSCAL catalog import via bridge `ImportCatalog`

- detection_tier_actual: integration
- detection_tier_target: integration

This is a `JUDGMENT` slice. The subjective calls below were made by the
implementing agent using best-reasoned, pattern-matched judgment against the
existing slices (006 scfimport, 030 oscal-bridge, 155 questionnaire) and the
constitutional invariants. None blocks merge; the maintainer iterates from the
"Revisit once in use" list once the product runs against real OSCAL catalogs.

## Decisions made

### D1 — Reconciliation of imported controls against SCF anchors: import-unmapped-and-flag

**Options considered.**

1. **Reject** an imported catalog whose controls have no obvious SCF crosswalk.
2. **Auto-map** via heuristic / AI similarity to the nearest SCF anchor.
3. **Import-unmapped-and-flag** — persist every imported control, recording a
   nullable `scf_anchor_id` (the SCF `scf_id` string, e.g. `"IAC-06"`), left
   `NULL` when no deterministic crosswalk exists, and surfaced as
   "needs operator mapping".

**Chosen: option 3.** This is the exact shape the spec proposes and it mirrors
the questionnaire slice (155): an imported row carries a nullable SCF-anchor
pointer, NULL = "needs mapping", and the operator resolves it later — the
"map once, canonical thereafter" pattern **minus the AI**. Rejecting (option 1)
would make the common case (a catalog that predates the operator's SCF mapping)
unusable; auto-mapping (option 2) would fabricate a control-coverage edge with
no human approval, which violates the AI-assist boundary spirit and invariant #7.

**Invariant #7 / P0-492-1 enforcement.** The mapping column holds the SCF
**anchor** identifier only — `imported_catalog_controls.scf_anchor_id` is a
free-form `scf_id` string (matching the questionnaire-slice precedent), and the
import path NEVER writes a requirement → requirement edge. An imported control
is a row in a distinct `imported_catalog_controls` table; it maps **to** an SCF
anchor or to nothing, never to another imported requirement. The bridge
projection carries each control's declared OSCAL `control-id`; we record it as
`source_control_id` (provenance) and as the candidate `scf_anchor_id` ONLY when
it exactly matches a known SCF `scf_id` for the current SCF framework version
(deterministic match, no inference). Otherwise `scf_anchor_id` stays NULL.

Confidence: **high** (direct pattern match to slice 155 + spec proposal).

### D2 — Provenance schema shape

Each import run writes one `imported_catalogs` row carrying:
`source` (constant `'oscal-import'`), `imported_by` (the operator id passed by
the CLI / caller), `source_sha256` (lowercase hex sha256 of the exact inbound
document bytes), `source_label` (the operator-declared framework label, e.g.
`"NIST SP 800-53 rev5"`), `oscal_version` (echoed from the validated document),
`control_count`, and `imported_at`. Each imported control row carries
`source_control_id` (the OSCAL `control-id`), `title`, `statement` (the OSCAL
control statement prose, concatenated), `group_path` (the `/`-joined OSCAL group
title chain for provenance of structure), and the nullable `scf_anchor_id`.

This mirrors the scfimport `Report` provenance fields and the walkthrough
content-hash discipline (sha256 over the exact bytes). The source hash makes a
spoofed/tampered catalog attributable after the fact (threat-model S/T).

Confidence: **high**.

### D3 — Document-size cap / control-count cap / parse timeout

- **Document-size cap: 16 MiB** (`MAX_CATALOG_BYTES`). A real NIST 800-53 rev5
  full catalog OSCAL JSON is ~3-4 MiB; 16 MiB is ~4× headroom while still
  bounding a billion-laughs-style expansion attempt (threat-model D/I). Enforced
  on the Go side (reject before the bytes ever cross the bridge) AND in the
  Python bridge (defense in depth — the bridge is the parser).
- **Control-count cap: 10,000** (`MAX_CATALOG_CONTROLS`). SCF itself is ~1,400
  controls; 800-53 rev5 is ~1,100. 10k is generous for any real framework and
  caps the import-transaction row count (threat-model D).
- **Parse timeout: 30 s** in the bridge (`IMPORT_PARSE_TIMEOUT_S`), matching the
  existing `bridgeRPCTimeout` on the Go client. trestle parse of a 4 MiB catalog
  is sub-second; 30 s is cold-process + CI-contention headroom.

These are constants, deliberately conservative, and called out in the revisit
list because the right values are an operations question once real catalogs flow.

Confidence: **medium** (the magnitudes are right; the exact numbers are a
first-pass that real usage will tune).

### D4 — Imported catalogs are a distinct table set, never the SCF spine (P0-492-4)

Imported controls land in NEW tables (`imported_catalogs` +
`imported_catalog_controls`), tenant-scoped under four-policy RLS. They do NOT
touch `scf_anchors` (the bundled, global, non-tenant SCF spine) on any path.
The importer has no UPDATE/INSERT against `scf_anchors`. This is the structural
guarantee behind P0-492-4: a spoofed "this is NIST 800-53" catalog can never
overwrite or shadow a bundled SCF anchor — it is a separate, provenance-labeled
tenant-scoped set that _points at_ SCF anchors.

Confidence: **high**.

### D5 — Role gate at the importer layer (`grc_engineer` / `admin`), AC-6 / P0-492-5

The import is a database operation invoked from the `atlas-oscal` CLI (like
`oscal-export` in `atlas-cli`), not an HTTP route behind `authzmw`. So the role
gate is enforced in the Go `Importer`: it takes the caller's role and refuses
with `ErrUnauthorizedRole` unless the role is `grc_engineer` or `admin` (admin is
the superset per the canvas 5-role model — admin can do anything `grc_engineer`
can). The CLI passes `--role` (defaulting to `grc_engineer`, since the CLI is an
operator tool run with DB credentials; the gate is the explicit, auditable
record of _which_ authority performed the import, and the tenant-scoped RLS +
`atlas_app` DB role is the second leg of defense). Anonymous / cross-tenant
import is impossible: the importer requires a tenant in context (RLS denies on a
missing tenant GUC) and a non-empty `imported_by`.

Confidence: **medium** (the _gate_ is right; whether the CLI should additionally
require an OAuth token rather than a `--role` flag is a revisit item once the CLI
grows real auth — today `oscal-export` runs the same way).

### D6 — Append-only audit log table for imports (AC-7)

`imported_catalog_audit_log` is a tenant-scoped, append-only (SELECT + INSERT
RLS only, FORCE ROW LEVEL SECURITY) table — the slice 011/013/019/035 append-only
precedent. One row per import attempt records `actor`, `source_sha256`,
`control_count`, `source_label`, and `action` (`catalog_imported` |
`import_rejected`). A rejected import (validation failure) STILL writes an
`import_rejected` audit row — but only after the transaction that would have
written the catalog rolls back, so the rejection is recorded without persisting
any catalog content (the audit write is a separate, committed transaction).

Confidence: **high**.

### D7 — `href` / external-resource non-dereference (P0-492-2 / threat-model I)

The bridge maps the trestle-parsed catalog into the projection by reading ONLY
in-document fields (control id, title, parts/prose, group titles). It never
follows `back-matter` `resource` `rlinks`/`href`, never opens a file path, and
the bridge process makes no outbound network call on the import path. A unit
test feeds an `href`-bearing catalog and asserts the projection is produced with
zero filesystem/network access (the test uses a sentinel `href` pointing at a
path that, if opened, would error — the import succeeds, proving no dereference).

Confidence: **high**.

## Revisit once in use

- **D3 caps** — re-tune `MAX_CATALOG_BYTES` / `MAX_CATALOG_CONTROLS` /
  `IMPORT_PARSE_TIMEOUT_S` against the largest real OSCAL catalog an operator
  actually imports (FedRAMP High baseline-resolved catalogs can be large).
- **D1 deterministic match** — once an operator-facing "map this imported control
  to an SCF anchor" UI exists (a follow-on), revisit whether the import should
  pre-populate `scf_anchor_id` from an exact `scf_id` match at all, or always
  leave it NULL for explicit operator confirmation.
- **D5 CLI auth** — when the `atlas-oscal` CLI gains real credential-based auth
  (OAuth device grant, like `atlas-cli login`), replace the `--role` flag with a
  token-derived role so the gate is not operator-asserted.
- **Group nesting** — `group_path` flattens the OSCAL group hierarchy to a
  `/`-joined string. If operators need the full nested group tree (for catalog
  navigation UI), promote it to a structured representation.
- **Statement concatenation** — the OSCAL `part`/`prose` tree is flattened to a
  single `statement` string. Revisit if downstream surfaces need the structured
  parts (e.g. assessment-objective parts).

### D8 — Coverage floor reflects bridge-INDEPENDENT coverage (CI reality)

The three load-bearing integration tests (AC-9/10/11) require BOTH a real
Postgres AND the Python compliance-trestle bridge. CI's integration shards do
not ship the Python bridge, so those tests `t.Skip()` in CI — the exact
slice-030 D2 precedent the existing `internal/oscal` package already lives under
(its floor of 69 likewise reflects bridge-skipped CI reality, not local
full-bridge coverage). The merged coverage gate therefore sees only this
package's bridge-INDEPENDENT branches (role gate, input validation, rejection
path, helpers): ~30.4%. The floor is set to **28** = floor(30.4 − 2), the
honest CI-achievable value. Locally, with the bridge present, the package
measures ~79%; the integration tests are the real safety net and were run green
locally against a real Postgres + real bridge before merge.

Confidence: **high** (matches the established slice-030 bridge-skip precedent).

## Detection-tier note

A real defect surfaced during the slice at the integration/CI tier: the first
push set the coverage floor (77) from LOCAL full-bridge coverage, but CI's
integration shard has no Python bridge, so the bridge-dependent tests skipped
and the merged gate measured only ~16% → gate failed. Caught at
`actual=integration` (the CI merged-coverage gate); the cheapest tier that
should have caught it is the same (`target=integration`) — it is precisely the
kind of environment-delta the integration gate exists to surface, and it was
fixed by setting the floor to the bridge-independent value + adding pure-Go unit
branches (D8). The transactional-rollback (AC-10) and tenant-isolation (AC-11)
guarantees are proven at the integration tier locally
(`target=integration, actual=integration`); the `href`-non-dereference guarantee
is a `manual_review` + Python-unit assertion (`target=manual_review`), no defect
found.
