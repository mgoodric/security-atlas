# Slice 471 — Role-scoped control-implementation checklist generator v0 — decisions log

**Type:** JUDGMENT (team-assignment heuristic + task-breakdown shape + citation strictness)
**Constitutional surface:** AI-assist boundary (hard) — the 5th AI-assist v0 surface (alongside 440/441/444).

This log records the build-time judgment calls. Per the JUDGMENT-slice convention
(no human sign-off gate), Claude made the subjective calls and recorded them here;
the maintainer iterates post-deployment.

---

## D1 — Team-assignment heuristic (the deterministic role-split)

**Decision.** The which-control → which-role split is DETERMINISTIC and lives in
`internal/checklist/roles.go` (`AssignRole`), never an LLM guess. It resolves a
control's free-text `owner_role` (+ `applicability_expr` as a fallback) to exactly
one of the FIXED v0 roles `{infra, engineering, security, unassigned}` via:

1. **Exact alias** on the normalized `owner_role` (lowercased, separators →
   spaces). The alias table (`ownerRoleAliases`) is seeded from common GRC role
   names + the demo dataset (slice 205) spellings — "infrastructure"/"platform"/
   "devops"/"sre" → infra; "engineering"/"developer"/"backend" → engineering;
   "security"/"infosec"/"grc"/"compliance"/"appsec" → security.
2. **Substring heuristic** (`substringHints`) in a fixed precedence order —
   security-flavored terms tested BEFORE the broader ops/eng terms so
   "security operations" maps to security, not infra.
3. **applicability_expr fallback** (`applicabilityFallback`) — only when
   `owner_role` is blank/non-indicative: a `data_class`/`pci`/`phi` predicate →
   security; a `product`/`application` predicate → engineering (tested BEFORE
   infra so "product" is not swallowed by infra's "prod" substring); a
   `cloud`/`env`/`network` predicate → infra. Conservative: it fires only on a
   clear signal.
4. **`unassigned` bucket** — surfaced honestly to the operator as a non-AI,
   non-approvable section listing the orphan controls; NEVER silently dropped
   (AC-1).

**Why deterministic-first.** The assignment is the auditable spine; keeping it out
of the LLM means a reviewer can reconstruct _why_ a control landed on a team, and
keeps the LLM surface minimal (it only writes task text). The map is the JUDGMENT
surface — exhaustively unit-tested (`roles_test.go`, 30+ enumerated cases incl.
normalization, precedence, fallback, and the unassigned bucket).

**Alternative rejected.** Letting the LLM both assign AND write tasks — rejected:
it makes the assignment opaque/unauditable and widens the hallucination surface to
the highest-stakes decision (who owns a control).

---

## D2 — Task-breakdown prompt shape

**Decision.** The LLM is invoked ONCE PER AI-ROLE (infra/engineering/security)
with that role's assigned controls, and asked to emit 1..N imperative task lines
per control, each line beginning with the control id in parentheses (the
grounding). The prompt (`prompt.go`):

- Constrains output to one task per line, control id cited verbatim, imperative
  voice, one sentence — so parsing (`parseTaskLines`) is deterministic.
- Bans marketing/superlative tone (mirrors the CLAUDE.md board-narrative ban list)
  for a consistent project-wide LLM voice.
- Marks a control with no evidence as `NO EVIDENCE YET` and instructs the model to
  write a task to ESTABLISH the evidence — never a claim that it exists (the
  no-fabricated-coverage guardrail spoken to the model; ALSO enforced structurally
  via the `no_evidence` flag, AC-6).

**Caps (D-mitigation, P0-471-7):** `MaxControls=60` per generation (reject over-cap),
`MaxTasksPerControl=5` (truncate over-cap lines), `MaxSectionTokens=1536`,
`GenerationTimeout=60s`. The token-budget + timeout ride the slice-498 shared
mandatory caps.

**Per-role (not per-control) generation.** One call per role rather than one per
control keeps the call count bounded (≤3 AI calls per generation) and lets the
model see the whole team's control set for coherent task phrasing.

---

## D3 — Citation strictness (the no-fabricated-coverage gate)

**Decision.** STRICT, mirroring slice 441's qaisuggest. Every generated task line
MUST cite its control id (the minimum grounding); it MAY additionally cite the
control's SCF anchor id and a linked policy id. The validator
(`citations.go::validateItemCitations`):

- **Grounding gate:** a cited UUID outside the control's grounding set
  (control id + linked policy ids) is a fabrication → reject the item.
- **Tenant-ownership gate:** every cited control/policy id must resolve to a
  tenant-owned row under RLS (`Store.ResolveControl`/`ResolvePolicy`); a
  cross-tenant id is RLS-invisible → reject.
- **SCF-anchor gate:** a cited `XXX-NN` token is allowed ONLY if it equals the
  control's own SCF id; any other anchor is out-of-grounding → reject.
- **A SINGLE unresolved/out-of-grounding citation suppresses the WHOLE role
  section** (the strict JUDGMENT call); nothing is persisted for a suppressed
  section (P0-471-2/P0-471-4). A no-fabricated-coverage invariant cannot be
  "mostly" honored.
- A line that cites NO control id at all → `no_citations` suppression.

**Why suppress the whole section, not just the bad item.** Consistency with slice
441/444 + the asymmetric cost: a checklist a security leader hands to a team is an
operational artifact; a single fabricated citation poisons trust in the whole
section. Cheaper to regenerate than to ship a "mostly-grounded" list.

---

## D4 — Adoption of the slice-498 shared guard + schema shape

**Decision.** The `checklist_sections` table is the APPROVABLE unit (per-section/
per-role approval, slice-182 D2). It carries the AI-assist boundary column set
(`ai_assisted`/`human_approved`/`human_approver` + `prompt_version`/`model_name`/
`model_version`/`model_provider`) and ADOPTS the shared
`ai_assist_human_approver_guard(...)` CHECK — the predicate is NOT re-authored
(P0-498-4), the migration calls the shared function, identically to slice 441's
`questionnaire_answers` migration. A second CHECK enforces provenance completeness
conditional on `ai_assisted` (the unassigned bucket is `ai_assisted=FALSE` and
exempt). `checklist_items` are the cited tasks (immutable; a non-empty-citations
CHECK guarantees presence). The append-only forensic record (system prompt +
context + raw draft) lives in the slice-498 `ai_generations` ledger
(`surface='checklist'`, `surface_subject=<section id>`) — already enumerated in the
slice-498 surface CHECK + the `internal/llm` `SurfaceChecklist` constant, so no
substrate change was needed.

**Why the section, not the item, carries the guard.** Approval granularity is
per-role; model provenance is one generation run shared across a section's items.
Putting the boundary columns on the section yields one approval row per role and
avoids per-item provenance duplication.

**Composite FK target.** `checklist_sections` gained `UNIQUE (tenant_id, id)` so
`checklist_items (tenant_id, section_id)` is a tenant-safe composite FK (slice-002
pattern). Surfaced by the local migration apply (the items FK failed without it);
fixed before any push.

---

## D5 — One-click per-section approval + non-bypassable export gate

**Decision.** A section persists `ai_assisted=TRUE, human_approved=FALSE`. Approval
(`Service.ApproveSection` → `Store.Approve`) is a SEPARATE operator action that
flips `human_approved=TRUE` + records the server-derived approver (`cred.ID`, NEVER
client-supplied — a caller cannot approve "as" someone else). The approve UPDATE's
`WHERE ai_assisted=TRUE AND human_approved=FALSE` means the unassigned bucket and
an already-approved section match no row → `ErrSectionNotFound`. The markdown
export (`ExportMarkdown` / `renderMarkdown`) renders ONLY approved AI sections and
returns 422 when zero are approved — a draft checklist cannot be exported
(P0-471-1, AC-11). The frontend `canExport` mirrors this so the button is disabled
until an approval lands. NO auto-approve path exists anywhere.

---

## D6 — CI model-stubbing approach (AC-17)

**Decision.** Integration + e2e tests use the slice-498 `llm.StubClient` (and, for
the multi-role split test, a small `perPromptStub` that cites the first control id
in the system prompt) so no live Ollama is needed in CI. The stub still runs the
shared request-validation (mandatory caps), so a malformed/over-cap request is
rejected identically to production. The Playwright e2e
(`checklist-generate-approve.spec.ts`) is fully hermetic — every
`/api/controls/checklist` BFF is `page.route()`-mocked (slice-594 discipline); the
real RLS + cross-tenant behaviour is the Go integration tier's job
(`internal/checklist/integration_test.go`). The real local-Ollama path is
exercised manually and documented (operator-docs quality caveat).

---

## Detection-tier classification (slice 353 Q-13)

- `detection_tier_actual`: `integration` — the one real bug surfaced during the
  slice (the missing `UNIQUE (tenant_id, id)` composite-FK target on
  `checklist_sections`) was caught when the migration was applied against real
  Postgres locally (the integration-parity step), before any push.
- `detection_tier_target`: `integration` — a composite-FK-target gap is exactly the
  class of schema bug the integration tier (migration apply + RLS round-trip) is
  designed to catch; pure-Go unit tests cannot see it. No coverage-tier gap: the
  bug was caught at its target tier.

A second pure-Go bug (the `applicabilityFallback` infra-"prod"-substring swallowing
"product") was caught at `detection_tier_actual = unit` / `target = unit` — the
exhaustive role-split table test fired on the first run. Correct tier.
