# 446 — decisions log: breach-disclosure 72h-handoff design spike (ADR-0017)

- detection_tier_actual: none
- detection_tier_target: none

(No bug surfaced during the slice. This is a decision-only spike — the deliverable
is [ADR-0017](../adr/0017-breach-disclosure-72h-handoff.md), a design document. No
runtime code, no migration, no schema change ships, so there is no bug surface; the
only verification surfaces are the docs gates [prettier / changelog].)

Parent: OQ #7 (privacy sibling-module resolution, incl. the one-sentence breach-handoff
sketch) + OQ #10 (disclosure/breach scope, deferred to phase 3). Foundation: slice 180
(privacy-module foundation — `subject_module` audit column, feature-flag module-toggling,
B1-B4 sibling discipline). Shape template: slice 400 / slice 455 (decision-spike + ADR
before build). **Held for maintainer review per P0-446-2 — does NOT auto-merge.**

## JUDGMENT calls made (the spike owns these; maintainer iterates post-merge)

### D1 — State machine: two orthogonal axes, not a single linear lifecycle

**Options:** (a) one linear lifecycle with `confirmed` as a stage you pass through;
(b) two orthogonal axes — a NIST 800-61r3 **lifecycle** axis (`detected` ... `closed`)
plus a **breach-determination** axis (`unassessed` / `suspected` / `confirmed` /
`not_a_breach`), with the handoff firing on `breach_determination -> confirmed`.

**Chosen:** (b). A single linear lifecycle mis-states the real semantics two ways: it
would either force "you cannot confirm a breach until contained" (wrong — GDPR Art. 33
awareness can precede containment) or treat `confirmed` as a transient stage (wrong — it
is a sticky, immutable determination). Orthogonal axes model "the response progresses
while the determination is set once." The lifecycle vocabulary is borrowed verbatim from
the slice-372 IR plan (`docs/governance/incident-response.md`) so the state machine
composes with the repo's existing canonical language rather than inventing one.

**Rationale anchor:** slice 372 NIST SP 800-61r3 lifecycle; GDPR Art. 33 "becoming aware."

### D2 — Clock ownership: split (security trigger / privacy deadline), single immutable anchor

**Options:** (a) security module owns the whole clock (determination + deadline);
(b) privacy module owns the whole clock; (c) split — security stamps the immutable
`breach_confirmed_at` trigger, privacy computes + stores `+72h` and tracks the deadline.

**Chosen:** (c). The breach determination is a _security_ judgment (is this a breach?);
the notification obligation is a _privacy_ judgment (whom, by when?). (a) pulls GDPR/HIPAA
notification logic into `security.*`; (b) lets privacy reach into the security response
lifecycle — both violate sibling isolation (OQ #7 B1/B3 spirit). The split keeps each
module owning what it is the system of record for. The deadline anchors to the security
`breach_confirmed_at` (the immutable legal-awareness moment), **not** to the privacy
record's creation time — so handoff lag (queue delay, privacy module toggled off/on)
cannot move a legally-fixed deadline. The deadline is computed **once and stored**, never
recomputed on read.

**Rationale anchor:** OQ #7 B1 (schema isolation) + Tampering threat (immutable clock start).

### D3 — Notification-target register: privacy-local, two entities, scale-bounded

**Options:** (a) model targets on the security incident; (b) model targets in `privacy.*`
as (i) a per-tenant `notification_authorities` register + (ii) a per-breach
`breach_affected_subjects` set.

**Chosen:** (b). Notification targets exist to satisfy GDPR Art. 33/34 + the HIPAA breach
rule — they are privacy primitives, so they live in `privacy.*` (RLS-scoped). Split into
a static-per-tenant authority register and a per-breach affected-subject set because they
have different cardinality and lifecycle: authorities are configured once and reused; the
affected-subject set is per-breach and **potentially millions of rows** (DoS + Information-
disclosure axes). The subject set holds a `subject_ref`, NOT a denormalized PII blob, and
the design explicitly names it scale-bounded (batched ingest, paginated reads) so the
implementation does not discover the bound at load. The security incident never knows who
gets notified — it only knows a breach was confirmed.

**Rationale anchor:** OQ #7 B1; STRIDE-I (minimum disclosure) + STRIDE-D (register bounding).

### D4 — Cross-module seam: route through `evidence.id`, not a direct FK

**Options:** (a) `privacy.breach_notifications` FK directly to `security.incidents.id`;
(b) the handoff crosses an `evidence.id` — the security confirm writes a
`security.breach_confirmation.v1` evidence snapshot, and privacy references **that**.

**Chosen:** (b). OQ #7 B3 literally forbids `privacy -> controls.id`; its _spirit_ is
"privacy never reaches into the security domain model." (a) would have privacy reach into
the security incident row — against the spirit even though `incidents.id` is not literally
`controls.id`. Routing through `evidence.id` (the project's canonical cross-module citation
currency — already how `privacy.processing_activities` cite security posture) keeps the
breach record security-local + mutable-by-security-only, gives privacy an immutable point-
in-time citation for notification artifacts, and reuses the exact seam slice 180 blessed.
What crosses: `evidence.id` + `tenant_id` + `breach_confirmed_at`. What does NOT cross:
`security.incidents.id`, `controls.id` (P0-446-4), the incident dossier, any PII.

**Rationale anchor:** OQ #7 B3 (letter + spirit); ADR-0012 (append-only evidence ledger as
the citation currency).

### D5 — Confirmation is human-only; security side is a NEW entity

**Calls bundled here (lower-contention):**

- **No automatic confirmation (P0-446-5 / STRIDE-E).** No rule engine, connector, or AI
  assist performs `suspected -> confirmed`. It is a deliberate, audited, high-privilege
  human action with `confirmed_by` recorded. The design has no automatic path to the
  legal-clock-start transition.
- **The breach record is a genuinely new `security.incidents` entity** — verified that no
  product-side incident table / package exists today (incidents arrive as
  `pagerduty.incident_summary` evidence). It is NOT a risk-register status extension (risk
  lifecycle is treatment-status `accept`/`transfer`/`mitigate`/`avoid`, not incident-
  lifecycle) and NOT the evidence ledger as system-of-record (evidence is the _input_ to a
  determination, not the determination).
- **Audit trail reuses slice 180's `subject_module`** verbatim — security writes `'core'`,
  privacy writes `'privacy'`; the full who-confirmed / who-notified chain is one cross-seam
  audit-log query. No new audit infrastructure.

**Rationale anchor:** P0-446-5; verified repo state (no `security.incidents` table in
`migrations/sql/`, no `internal/api/incident` package); slice 180 `subject_module`.

## Recommendation + phase

**ADOPT-DEFERRED, confidence HIGH.** Confirms OQ #10's phase-3 / privacy-v0 timing. The
**handoff design** is specified; the **workflow build** stays gated on (a) maintainer
approval of ADR-0017 and (b) the OQ #7 privacy-v0 demand trigger. This ADR does not
advance the build timing (P0-446-3) and does not resolve OQ #10 (it stays open).

## Scope-discipline confirmations (P0 checklist)

- **P0-446-1** — no production code: ADR + decisions log + canvas pointer + CHANGELOG only.
  No migration, no Go package, no endpoint, no `privacy.*` schema created. Schema sketch is
  ADR _content_, not DDL. ✓
- **P0-446-2** — does NOT auto-merge: ADR Status is "Proposed", PR body flags hold-for-review. ✓
- **P0-446-3** — does NOT decide privacy-module ship timing: stays OQ #7's "v0 on prospect demand". ✓
- **P0-446-4** — no `privacy -> controls.id` reference in the sketch: seam crosses `evidence.id` only. ✓
- **P0-446-5** — no automatic breach-confirmation: human-initiated, audited, `confirmed_by` required. ✓

## Revisit list (deferred to the implementation slice — ADR § Consequences)

Notification UX; HIPAA 60-day individual + HHS-notification branching; AI-assist for
notification drafting (falls under the CLAUDE.md AI-assist boundary if it ever lands);
multi-jurisdiction lead-DPA / one-stop-shop selection; confirming `security.incidents`
does not subsume the slice-372 maintainer IR runbook (different surfaces).

## Follow-on slices (filed ONLY after maintainer approves ADR-0017)

Per AC-5: (1) breach state-machine + `security.incidents` entity; (2) 72h-clock +
`privacy.breach_notifications`; (3) notification-target register; (4) security↔privacy
handoff wiring. All `not-ready` until approval + the privacy-v0 demand trigger. NOT filed
by this slice.
