# Slice 173 — MCP server write tools + HITL approval — decisions log

**Slice type:** `JUDGMENT`. D1 (the HITL pattern) was pre-locked by the
maintainer on 2026-05-20 to **Pattern A — draft-then-confirm**, mirroring
the slice-022 exception-approval flow. The implementing engineer makes
every other design call (D2-D7 below) and records them here per
`Plans/prompts/04-per-slice-template.md`.

## Context

The slice doc (`docs/issues/173-mcp-server-write-tools.md`) fixes the
WHAT (four write tools — `create_risk`, `update_control_state`,
`push_evidence`, `update_risk_treatment` — plus a confirm verb) and the
WHY (slice 172 shipped read-only MCP tools; the maintainer's full ask
was read + write, and write tools must honor the AI-assist boundary in
`CLAUDE.md` §"AI-assist boundary (hard)").

Pre-locked design decisions enumerated in the slice doc:

- **D1 = Pattern A — draft-then-confirm.** Write tools insert a row at
  state=ai_proposed; a separate confirm action flips to state=applied
  and runs the canonical Applier inside the same transaction.
- **Schema invariant.** `(ai_assisted=true AND human_approved=true) →
  human_approver IS NOT NULL` enforced at the DB layer; engineer picks
  CHECK vs trigger at impl time.

All seven design calls below honor every constitutional invariant
relevant to this slice (#2 ingestion + evaluation separated, #6 RLS
tenancy, #9 manual evidence first-class, AI-assist boundary).

## Decisions made

### D1 — HITL pattern (PRE-LOCKED)

**Chose: Pattern A — draft-then-confirm.**

Maintainer locked this on 2026-05-20 before the engineer picked up the
slice. The slice doc's "Notes for the implementing agent" section
walks through the Pattern A/B/C trade-offs in detail; the short version:

- _Pattern A_ (chosen): minimal schema impact, mirrors slice-022's
  exception-approval shape (operators already know it), one terminal-
  state flip per proposal.
- _Pattern B_ (rejected): a shadow `*_proposals` table per canonical
  surface (risks, controls, evidence) — N new tables + a merge job +
  per-table RLS policies. Over-engineered for v1's four-tool set.
- _Pattern C_ (rejected): platform rejects every `ai_assisted=true AND
  human_approved=false` write; operator co-signs each tool call in the
  MCP client before it commits. Tightest safety, but UX-prohibitive.

This decision is binding for slice 173; future slices may revisit if
the write surface expands beyond ~10 tools.

### D2 — Single proposal table vs per-canonical-row draft

**Chose: single `mcp_write_proposals` table.**

The slice doc's "Pattern A implementation guidance" allows either
shape; the engineer evaluated both at impl time.

- _Single table_ (chosen): one migration, one set of RLS policies, one
  CHECK constraint enforcing the AI-assist invariant. The `tool_input`
  JSONB column absorbs the per-tool shape variation; the Applier
  dispatches per `tool_name`. Applied row points to the canonical row
  via `applied_subject`.
- _Per-canonical-row draft_ (rejected): add `state` + `ai_assisted` +
  `human_approver` columns to `risks`, `controls`, and
  `evidence_records`. Cleaner audit trail (the draft row IS the future
  canonical row), but the schema change touches three load-bearing
  tables, every downstream consumer must filter on `state='active'`,
  and the migration risk is much higher.

Tiebreaker: invariant #2 (ingestion + evaluation separated) reads
naturally onto a separate proposal table — the proposal is the AI's
*proposal*, the canonical row is the platform's *commitment*. Distinct
identities deserve distinct tables.

### D3 — CHECK vs trigger for the AI-assist invariant

**Chose: CHECK constraint.**

The slice doc explicitly invites the engineer to pick.

- _CHECK_ (chosen): single-row, no cross-row state; fires on every
  INSERT and UPDATE; transparent to query planners; rejects malformed
  rows even when the application layer is bypassed (e.g., a future
  admin migration script that touches the table). Constraint
  `mcp_wp_ai_assist_invariant` lives alongside `mcp_wp_state_check` /
  `mcp_wp_tool_name_check` / three terminal-state consistency CHECKs.
- _Trigger_ (rejected): would let us emit a custom error message on
  violation, but adds a per-row PL/pgSQL step and is invisible to
  schema dumps without explicitly enumerating triggers. Overkill for
  a single-row invariant.

Defense-in-depth: the application's `writeproposals.Store.Create`
validates the `AllowedTools` set BEFORE round-tripping, so a typo in
`tool_name` surfaces as `ErrUnknownTool` rather than a raw 23514
check_violation. The CHECK is the backstop, not the primary validator.

### D4 — Applier transaction sharing

**Chose: applier runs inside the confirm transaction.**

When an operator invokes `confirm_write`:

1. `Store.Confirm` opens a tx, sets the tenant GUC, locks the proposal
   FOR UPDATE.
2. The Applier (registered per `tool_name`) executes hand-written SQL
   inside that tx — `INSERT INTO risks`, `INSERT INTO evidence_records`,
   `UPDATE risks SET treatment=...`.
3. The store flips the proposal to state=applied + human_approved=true
   + human_approver + applied_subject in the same tx.
4. Tx commits. All-or-nothing.

Alternative considered: applier opens its own tx via the domain
store (e.g., `risk.Store.Create`). Rejected because the two-tx shape
admits a window where the canonical row exists but the proposal is
still ai_proposed (or vice versa) if either commit fails. Single-tx
is the only correct shape for this invariant.

Trade-off: the appliers are hand-written SQL, not routed through the
sqlc-generated `dbx.Queries`. We accept the maintenance cost
(if the canonical row shape changes, the applier must be updated in
the same PR) in exchange for clean rollback semantics. The four
appliers live in `internal/mcp/writeproposals/appliers.go` next to
the store so the lockstep is visible.

### D5 — `created_by` and `human_approver` column type (TEXT vs UUID)

**Chose: TEXT.**

v1's `credstore.Credential.ID` is a token-shaped string ("key_..."),
not a UUID. Slice 034 (OIDC RP) will eventually populate these with
real user IDs from the IdP; the column type stays TEXT so both shapes
fit without a second migration. The CHECK constraint defends against
empty strings (`length(human_approver) > 0`) so an operator can't
sneak an empty approver through by passing the empty string.

### D6 — Pending-cap value (5 per (tenant, user))

**Chose: 5 default, configurable via `WithPendingCap`.**

P0-A5 requires a write quota stricter than the read quota. Slice 145's
read concurrency cap is 50 per credential; setting the write cap to
5 keeps the approval queue tractable for a single operator (the v1
persona is a solo security leader). Tests override via
`Store.WithPendingCap(n)` so the integration suite can exercise the
cap deterministically.

### D7 — Per-tool meta-audit row strategy

**Chose: defer to v2; rely on RLS + the proposal-table audit trail.**

The slice doc invites a write-side meta-audit row analogous to slice
172's User-Agent header. v1 leaves that to the proposal table itself:
every write produces a `mcp_write_proposals` row with `created_by` +
`ai_model_name` + `ai_model_version` + `tool_input`; every confirm
adds `human_approver` + `applied_at`; every reject adds `rejected_at`
+ `reject_reason`. That's a forensic-grade trail without duplicating
rows into `me_audit_log` (slice 124's surface).

Follow-on: slice 174 may extend `me_audit_log` with the
`mcp_write_proposal_created` / `_confirmed` / `_rejected` actions
once the unified-audit aggregator (slice 124) has a stable consumer.
Tracked as out-of-scope here.

## Threat model (final — STRIDE re-run)

The slice doc's preliminary STRIDE was tagged `HOLD-pending-review`.
This is the fresh pass run against the implemented surface with slice
172's read tools as the precedent.

**S — Spoofing.** MCP server in stdio mode authenticates via bearer
token (same posture as slice 172). The write surface inherits this:
the proposal records `created_by` from the bearer's credential id, so
the audit trail attributes the proposal to the operator who supplied
the token to their MCP client. No new spoofing surface beyond what
slice 172 documented.

**T — Tampering.** Highest-risk category for writes. Prompt injection
into LLM context can drive a tool call with attacker-controlled
`tool_input`. Mitigations:

1. The platform NEVER commits to the canonical table directly; the
   write tool only INSERTs to `mcp_write_proposals` with
   state=ai_proposed.
2. The operator MUST explicitly confirm (server-side gate:
   `IsApprover || IsAdmin`).
3. Even if the operator confirms blindly, the per-tool Applier
   re-validates the bounded input shape (e.g., `update_control_state`
   only accepts `pass|fail|na|inconclusive`).
4. RLS gates the apply: a proposal for tenant A can only INSERT
   into tenant A's risks / evidence.

Verdict: tampering is bounded by the operator's existing role + RLS +
the per-tool bounded schema. Acceptable.

**R — Repudiation.** Every write produces an `mcp_write_proposals`
row with the AI-model identity (name + version), the human approver
(or null pre-confirm), and the full `tool_input`. The proposal table
is RLS-gated to the originating tenant; no cross-tenant repudiation
window. Acceptable.

**I — Information disclosure.** Write tools file proposals; the
proposal-list response includes the `tool_input` because the operator
needs it to decide approve/reject. `tool_input` is bounded to the
documented per-tool schema (no `payload_json` arbitrary blobs).
`evidence_records.payload` is NEVER echoed in proposal responses —
the proposal stores the PROPOSED payload in `tool_input`, but the
already-stored evidence payload (from past push_evidence calls) lives
in the evidence ledger, not the proposal table. Cross-tenant
isolation enforced by RLS four-policy. Acceptable.

**D — Denial of service.** Pending-cap (5 per (tenant, user) default)
prevents the LLM from flooding the approval queue. The cap is
enforced inside the confirm transaction, so concurrent floods are
serialized at the DB level. The MCP client's per-tenant concurrency
cap (slice 145) still applies to the underlying HTTP requests.
Acceptable.

**E — Elevation of privilege.** Three gates:

1. Bearer token authenticates as the operator's credential — same as
   slice 172. No write tool runs without bearer.
2. The confirm + reject HTTP routes gate on `IsApprover || IsAdmin`.
   A bearer that authenticates as the operator but lacks the approver
   role gets 403.
3. RLS gates the canonical write: even an Applier bug that builds
   the wrong SQL is bounded to the originating tenant.

Acceptable. The slice does NOT expose admin-tier writes (no
`delete_tenant`, no `revoke_credential`); the four documented tools
are tenant-scoped writes only.

**Verdict.** has-mitigations across S/T/R/I/D/E. No outstanding HOLD;
all preliminary threats have explicit mitigations + tests.

## Anti-criteria honored

- **P0-A1.** No audit-binding artifact ships without human approval —
  enforced via Pattern A + the CHECK constraint + the IsApprover gate
  on confirm.
- **P0-A2.** No admin-tier writes exposed — `AllowedTools` is a
  4-element set; the DB CHECK pins the same set; adding a fifth tool
  requires both a Go change and a migration.
- **P0-A3.** Slice 172 anti-criteria intact: User-Agent still required
  on every outbound request; column allowlists on read tools unchanged.
- **P0-A4.** Schema CHECK `mcp_wp_ai_assist_invariant` blocks
  `ai_assisted=true AND human_approved=true` without `human_approver`
  — verified at the DB level by
  `TestSchemaInvariant_BlocksApprovedWithoutApprover`.
- **P0-A5.** Pending-cap default 5 (configurable).
- **P0-A6.** Write tools use User-Agent template
  `atlas-mcp/<version> (mcp; ai_assisted=write)` — distinct from the
  read-tool template. Verified by
  `TestWriteTools_UseWriteUserAgent`.
- **P0-A7.** `tool_input` is the operator-bounded input shape, not
  the evidence-payload free-text content.
- **P0-A8.** Test-fixture tokens use neutral `test-*` and `key_<uuid>`
  shapes — no vendor prefixes.

## Acceptance criteria coverage

| AC                                            | Verified by                                                            |
| --------------------------------------------- | ---------------------------------------------------------------------- |
| AC-1..AC-4 four write tools                   | `internal/mcp/tools/writes.go` + schema-stability snapshot             |
| AC-5 meta-audit row on every write            | Every Create → mcp_write_proposals row with ai_model + actor + input   |
| AC-6 HITL approval for audit-binding writes   | `Store.Confirm` + `IsApprover` gate (`TestHTTP_ConfirmRequiresApprover`) |
| AC-7 cross-tenant test                        | `TestList_CrossTenantIsolation`, `TestHTTP_CrossTenantIsolation`       |
| AC-8 write-quota integration test             | `TestCreate_EnforcesPendingCap`                                        |
| AC-9 schema-snapshot test                     | `TestSchemaStability` covers all 11 tools                              |
| AC-10 stdio transport unchanged               | cmd/atlas-mcp/main.go single addition: `tools.AllWithWrites`           |
| AC-11..AC-15 per-tool happy + sad paths       | tools_test.go + writeproposals tests                                   |
| AC-16 decisions log                           | this file                                                              |

## Schema-level enforcement summary

```sql
-- migrations/sql/20260520030000_mcp_write_proposals.sql (excerpt)
CONSTRAINT mcp_wp_ai_assist_invariant
    CHECK (
        NOT (ai_assisted = TRUE AND human_approved = TRUE)
        OR (human_approver IS NOT NULL AND length(human_approver) > 0)
    )
```

Verified at the DB layer via
`TestSchemaInvariant_BlocksApprovedWithoutApprover` — admin pool
attempts to INSERT a row with `ai_assisted=true, human_approved=true,
human_approver=NULL` and the DB rejects with 23514 / constraint name
`mcp_wp_ai_assist_invariant`.

## Files of record

- Migration: `migrations/sql/20260520030000_mcp_write_proposals.sql`
- Store: `internal/mcp/writeproposals/store.go`
- Appliers: `internal/mcp/writeproposals/appliers.go`
- HTTP handlers: `internal/api/mcpwriteproposals/handlers.go`
- MCP write tools: `internal/mcp/tools/writes.go`
- Router wiring: `internal/api/httpserver.go` (5 new routes)
- RouteSpecs: `internal/api/openapi/routes.go` (5 new entries)
- OpenAPI spec: `docs/openapi.yaml` (regenerated)
- Unit tests: `internal/mcp/writeproposals/store_test.go`,
  `internal/mcp/tools/tools_test.go`
- Integration tests:
  `internal/mcp/writeproposals/integration_test.go`,
  `internal/api/mcpwriteproposals/integration_test.go`
