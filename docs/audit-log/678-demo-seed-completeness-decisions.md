# Slice 678 — Demo seed completeness: decisions log

**Type:** JUDGMENT (how much demo breadth to demonstrate)
**Scope:** `internal/demoseed` data-only additions (org_units + risk→org_unit/theme
linkage, a Decision Log, a questionnaire, role-holder users + api_keys for the
policy-ack roster). No migration, no wire change.

- detection_tier_actual: none
- detection_tier_target: none

(No bug surfaced during the slice. The previously-empty surfaces were a
seed-coverage gap — a missing-data condition, not a defect in a tier — caught by
the 2026-06-10 demo-tenant audit, ATLAS-028 + ATLAS-037, before this slice.)

---

## Demo-breadth scope decision (AC-5)

The JUDGMENT call this slice owns: **which headline surfaces MUST be demonstrable
for the v1 "diligence the diligence tool" tour, vs. which empty states are
acceptable to leave for later.** The binary v1 success test is whether the solo
security leader runs their next SOC 2 audit + board pack out of security-atlas
without reaching for Vanta or a sheet. A buyer doing diligence on the tool itself
tours the seeded demo; an empty headline panel reads as "the product can't do
this," which is worse than a populated-but-fictional one.

**Decision: a surface is in-scope for the seed iff a buyer-tour click reaches it
from a primary nav item AND it currently renders an empty/zero state.** The audit
named two such clusters; this slice closes both:

| Surface (nav click)                        | Verdict    | Why                                                                                                 |
| ------------------------------------------ | ---------- | --------------------------------------------------------------------------------------------------- |
| `/risks/hierarchy` org tree                | MUST seed  | Top-level Risks sub-nav; "No org_units yet" dead-ends the whole hierarchy view.                     |
| `/risks/hierarchy` theme heatmap           | MUST seed  | Same page; needs risks with `org_unit_id` + `themes` (both were unset).                             |
| `/risks/hierarchy` decision timeline       | MUST seed  | Same page; "No decisions recorded yet" is the third empty panel on one screen.                      |
| `/questionnaires`                          | MUST seed  | Top-level nav item; "Upload your first…" reads as an unbuilt feature.                               |
| Policy acknowledgment roster (5 policies)  | MUST seed  | Each policy detail shows "no required-role users" — the ack feature looks inert.                    |
| Framework posture (dashboard)              | COORDINATE | Seed ensures `framework_versions` are active; slice 671 (MERGED) runs the evaluation that fills it. |
| Aggregation rules (`/risks/hierarchy`)     | DEFER      | The manual heatmap + timeline carry the demo; a seeded rule adds engine complexity, not tour value. |
| Tenant-private `org_themes`                | DEFER      | The 10 built-in themes (global, tenant_id IS NULL) populate the heatmap axis with zero extra rows.  |
| Multi-questionnaire / AnswerLibrary priors | DEFER      | One demonstrable questionnaire proves the surface; a second adds rows, not narrative.               |

---

## Decisions made

### D1 — Org hierarchy shape: fixed 3-tier (1 company → 3 org → 6 team)

- **Options:** (a) one flat company unit; (b) a deep arbitrary tree; (c) a
  realistic 3-tier company→org→team shape.
- **Chosen:** (c). Canvas §6.4 defines the three `risk_level` tiers
  (company/org/team) and says risks file at the team level by default. A 3-tier
  shape exercises the org-tree renderer's nesting AND gives the heatmap multiple
  org_unit axes. Six team leaves spread the 20 risks ~3 per node.
- **Fixed, not scaled:** the org tree's demo value is "renders a real hierarchy,"
  independent of `--scale`. At 0.1x it must still render — so org_units bypass
  `applyScale` (same rationale for decisions + questionnaire). The populated-tenant
  guard counts only controls/risks/evidence_records, so these fixed rows don't
  affect it.
- **Confidence:** high.

### D2 — Heatmap themes: reference the 10 built-in `org_themes`, seed none

- **Options:** (a) seed tenant-private themes; (b) reference the global built-in
  slugs (tenant_id IS NULL, migration `20260511000015`).
- **Chosen:** (b). The built-ins are visible to every tenant via the
  `tenant_or_catalog_read` policy, and `RiskThemeOrgUnitGrid` joins
  `risks.themes` → `org_themes.theme_name`. Tagging each risk with 2 built-in
  slugs populates the grid with zero extra rows. A risk needs BOTH a non-NULL
  `org_unit_id` AND a non-empty `themes` array to contribute a cell — the prior
  seed set neither.
- **Confidence:** high.

### D3 — Decision Log: 4 active entries, a subset linked to risks

- **Options:** (a) one decision; (b) a handful spanning the constraint
  vocabulary, some linked to seeded risks via `decision_risks`.
- **Chosen:** (b). Four entries span the canvas §6.7 constraint tags
  (time-pressure, cost, dependency-blocked, risk-accepted) and the
  decision-maker roles. Two link to a seeded risk so the timeline-to-risk
  resolution is demonstrable; two are standalone (e.g. a tech-stack deferral).
  All ship `status='active'` — `superseded` would require a `superseded_by`
  self-ref and adds no tour value.
- **Confidence:** high.

### D4 — Questionnaire: one CAIQ-style instance, 6 questions, 5 answered

- **Options:** (a) a fully-answered questionnaire; (b) a partially-answered one
  with one unmapped question.
- **Chosen:** (b). A 100%-answered form reads as unrealistic; leaving one
  question `scf_anchor_id IS NULL` + unanswered shows the realistic "needs
  mapping / in-progress" state the slice-155 tracer-bullet was built around.
  SCF anchors are free-form scf_id strings (`IAC-06`, `CRY-05`, …) per the
  slice-155 schema (no FK).
- **Confidence:** medium — the question text + anchor mapping is fictional
  control-text authorship; a real auditor may map differently. (Revisit.)

### D5 — Policy-ack roster: model the denominator through `api_keys`

- **Context:** the roster denominator query (`CountRequiredRoleUsersForVersion`)
  counts distinct `api_keys.issued_by` whose `owner_roles` intersect the policy's
  `acknowledgment_required_roles` (or `is_admin`). This is the slice-023 stand-in
  until slice-035 OPA-RBAC graduates it.
- **Chosen:** seed 7 role-holder users, each with an `api_keys` row carrying
  `owner_roles`. Every user holds the org-wide `"employee"` role; a subset hold a
  category role (`security-engineering`, `it-operations`, …). Every published
  policy requires `["employee", <category>]`, so every policy's denominator is
  non-zero and the per-policy roster sizes differ. A subset of users (`Acks=true`)
  write `policy_acknowledgments`, so the numerator is non-zero but not 100% — a
  partial roster reads more honestly than all-or-nothing.
- **api_keys `token_hash` is a non-functional sha256 digest** of
  `"demo-api-key:"+uuid` — it exists ONLY to populate the roster denominator; it
  is never a presentable bearer token (the preimage isn't a validly-formatted
  token). Teardown sweeps these rows.
- **Confidence:** medium — the roster is wired through the slice-023 `api_keys`
  stand-in; when slice-035 lands a real user-role binding table the seed must
  follow. (Revisit.)

### D6 — Role users carry `demo_only=TRUE`, reuse the admin password hash

- Role users never log in interactively (the operator logs in as
  `admin@demo.example`). They exist as complete, RLS-consistent records to back
  the roster. Reusing the admin's argon2id hash avoids generating + discarding 7
  more passwords. `demo_only=TRUE` carries the slice-205 forensic mark and the
  slice-142 guard that refuses promoting a demo user to super_admin.
- Emails are `first.last@demo.example` — distinct from the admin
  (`admin@demo.example`) and the attester shape (`first@demo.example`) so
  `users_email_per_tenant_unique` never collides.
- **Confidence:** high.

### D7 — Teardown extended to sweep the new tables

- `Seeder.Teardown` now deletes `decision_*` links → `decisions`,
  `questionnaire_answers` → `_questions` → `questionnaires`, `org_units` (after
  risks; `risks.org_unit_id` is ON DELETE SET NULL), `api_keys`, and relies on the
  existing `policy_acknowledgments` sweep (already before `policies`, honoring its
  ON DELETE RESTRICT FK). `TestSeedTeardown_RoundTrip` pins every new table back to
  zero (added to `allSeededTables`).
- **Confidence:** high — round-trip test passes against a real Postgres.

---

## Revisit once in use

1. **D4 (low/medium):** the questionnaire's 6 questions + their SCF-anchor
   mappings are fictional control-text authorship. Re-review the anchor mappings
   (`IAC-06`, `CRY-05`, `BCD-11`, `TDA-09`, `TPM-03`) once a real CAIQ/SIG import
   exists, and consider whether the unanswered `GRC-09` question should map to a
   real anchor.
2. **D5 (medium):** the ack roster is modeled through the slice-023 `api_keys`
   stand-in. When slice-035 (OPA-RBAC) introduces a real user-role binding table,
   re-point the seed's role assignment at it and drop the synthetic `api_keys`
   rows. Re-check that `acknowledgment_required_roles` on the seeded policies still
   intersects the new role vocabulary.
3. **D1/D3 demo realism:** the org-unit names, decision narratives, and
   constraint tags are plausible but invented. Re-evaluate against a real
   reference customer's org chart + decision log once one exists; the heatmap's
   severity distribution (driven by the existing risk inherent_score pattern) may
   want hand-tuning so the demo shows a believable hot/cold spread rather than a
   uniform grid.
4. **Framework posture (AC-4):** this slice only ensures the demo
   `framework_versions` are `status='current'`. Confirm slice 671's evaluation
   actually fills the dashboard "Framework posture" tiles for the demo tenant end
   to end once both are on `main` together; if the tiles stay empty, the gap is in
   671's evaluation trigger, not the seed.
5. **Scale interaction:** org_units/decisions/questionnaire are fixed-count (not
   scaled). If an operator runs `--scale 5` for a large screenshot, these surfaces
   stay small while controls/risks/evidence grow 5×. Re-evaluate whether the org
   tree should gain leaves proportionally at high scale (probably not — but worth a
   look once someone screenshots at scale).

## Confidence summary

| Decision                   | Confidence |
| -------------------------- | ---------- |
| D1 org hierarchy shape     | high       |
| D2 built-in themes         | high       |
| D3 decision log            | high       |
| D4 questionnaire content   | medium     |
| D5 ack-roster via api_keys | medium     |
| D6 demo_only role users    | high       |
| D7 teardown extension      | high       |
