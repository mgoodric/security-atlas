# ADR 0017 — Security-to-privacy breach-disclosure 72-hour notification handoff

**Status:** Proposed — **ADOPT-DEFERRED** (recommendation pending maintainer sign-off; this ADR is the slice-446 decision gate that specifies the design before any breach-workflow code is filed).

**Date:** 2026-06-12

**Slice:** 446 (`docs/issues/446-breach-notification-workflow-spike.md`).

**Decision-only spike.** This ADR ships no production code (slice 446 P0-446-1). It does not add a migration, a Go package, or an endpoint. It resolves the design gap that [open question #10](../../Plans/canvas/11-open-questions.md) ("Disclosure / breach-notification workflow scope") deferred and that [OQ #7's resolution](../../Plans/canvas/11-open-questions.md) only sketched: the **security to privacy "breach-confirmed to 72-hour notification" handoff**. The recommendation, its confidence, and the single decision the maintainer must make are stated in [§ Decision](#decision).

**Does NOT resolve OQ #10.** The notification workflow itself stays phase-3 / privacy-v0 work (OQ #10 still open; P0-446-3 unchanged). This ADR specifies the **handoff design** so the eventual implementation slice inherits a settled state-machine + seam, the way [ADR-0016](0016-oidc-identity-for-keyless-signing.md) settled the cosign-keyless identity before slice 414 builds it.

**Slot note:** the next free sequential ADR slot is **0017**. Slots 0001-0016 are occupied. (0003 is a pre-existing double-occupancy: `0003-audit-period-freeze-hash-inputs.md` + `0003-oauth-authorization-server.md`; this ADR does not touch that collision.)

---

## Scope precision (read this first)

Two distinct "incident" surfaces exist in this project. **Conflating them is the error this section exists to prevent.**

| Surface                                  | What it is                                                                                                       | Where it lives                                                                | This ADR's surface? |
| ---------------------------------------- | ---------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------- | ------------------- |
| **The project's own incident response**  | How the security-atlas _maintainer_ responds to a compromise of the project itself                               | `docs/governance/incident-response.md` (slice 372), NIST SP 800-61r3          | **NO**              |
| **A tenant's breach inside the product** | A security-atlas _operator_ recording a confirmed breach of THEIR systems + notifying regulators / data subjects | A future `security.*` incident entity + the `privacy.*` notification workflow | **YES**             |

This ADR is about the **second row only** — the product feature an operator uses. It borrows the slice-372 IR plan's NIST SP 800-61r3 lifecycle vocabulary (`detect -> triage -> contain -> eradicate -> recover -> review`) because that vocabulary is already canonical in the repo, but the entity it governs is the operator's tenant-scoped breach record, not the maintainer's project-incident runbook.

---

## Context

### What is unspecified today

[OQ #7](../../Plans/canvas/11-open-questions.md) resolved the **sibling-module** architecture: privacy primitives live in their own Postgres `privacy.*` schema, sharing auth / tenancy / RLS / audit-log / evidence-citation infrastructure, with four locked sub-decisions (B1 schema isolation, B2 shared infrastructure, B3 cross-module reference seam, B4 lint enforcement). Slice 180 landed the **foundation** (the audit-log `subject_module` column, the `module:<name>:enabled` feature-flag pattern, the CONTRIBUTING.md sibling-discipline doc).

The OQ #7 resolution sketched the breach handoff in one sentence: _"incident lives in security module; 72-hour notification workflow lives in privacy module; structured handoff at the 'breach confirmed' state transition."_ That sentence is the entire specification today. Four load-bearing questions are unanswered:

1. **The state machine.** What states does a breach move through, and at which transition does the handoff fire?
2. **Clock ownership.** Which module owns + tracks the GDPR Art. 33 72-hour deadline, and what immutable event starts it?
3. **The notification-target register.** How are supervisory authorities + affected data subjects modeled, and in which schema?
4. **The cross-module seam.** What IDs cross the security/privacy boundary, given the B3 rule that privacy MAY reference `evidence.id` / `policy.id` but MUST NOT reference `controls.id` directly?

### What already exists (the substrate the design must respect)

The verified state of the repo, not assumptions:

- **No product-side incident entity exists yet.** The security module today has the risk register (`risks`, slice 019) and the evidence ledger. Operational incidents arrive as **evidence** (`pagerduty.incident_summary`, `datadog.siem_signal` schemas). There is no `security.incidents` table and no `internal/api/incident` package. So the security side of the handoff is **new** — the implementation slice creates it; this ADR specifies its shape.
- **The audit-log carries `subject_module`** (slice 180) on all nine audit-log tables, defaulting to `'core'`. A privacy-side write sets `subject_module='privacy'`; a security-side write sets `'core'`. The handoff's audit trail uses this column verbatim — the seam is auditable on both sides with no new audit infrastructure.
- **RLS is enforced at the DB layer** (invariant #6, [ADR-0011](0011-rls-tenant-isolation.md)) via `current_tenant_matches(tenant_id)`. Any new `privacy.*` breach entity inherits the same four-policy split, exactly as slice 180 asserted the `subject_module` column did not weaken.
- **The risk register has no `incident`/`breach` status today** — its lifecycle is treatment-status (`accept` / `transfer` / `mitigate` / `avoid`), not an incident lifecycle. So the breach state machine is not a risk-status extension; it is a distinct entity.
- **The B3 seam** is `privacy.processing_activities MAY reference evidence.id + policy.id, MUST NOT reference controls.id`. The privacy/security mapping happens at the framework-satisfaction layer (GDPR Art. 32), not at the data-flow layer.

### The constraints that bound the design

1. **Sibling-module isolation (OQ #7 B1).** The 72-hour notification workflow and its target register are **privacy primitives** — they live in `privacy.*`, not `security.*`. The breach record itself is a **security primitive** — it lives in `security.*`. The handoff crosses a schema boundary by construction.
2. **The B3 reference direction (OQ #7 B3).** B3 is literally about `controls.id`, but its _spirit_ is "privacy does not reach into the security control graph." The cleanest design extends the spirit: the privacy notification workflow references an **evidence snapshot** of the breach determination (`evidence.id`), not the `security.incidents.id` row directly — keeping the breach record security-local and the privacy side citation-shaped.
3. **The clock is legally load-bearing.** GDPR Art. 33 gives 72 hours from "becoming aware." Its start time must be immutable once set (Tampering threat), and a missed deadline must not be retroactively concealable (Repudiation threat).
4. **Breach-confirm is a deliberate, high-privilege, human action (P0-446-5).** It starts a legal clock; it is never automatic. No rule, no connector, no AI assist confirms a breach.
5. **Phase timing (OQ #10).** The workflow is phase-3 / privacy-v0 work. This ADR specifies the design; it does not advance the build timing (P0-446-3).

---

## The design

### 1. The state machine

The breach record is a **new `security.*` entity** (working name `security_incidents`) whose lifecycle borrows the slice-372 NIST SP 800-61r3 vocabulary, with one determination overlaid: **breach-confirmed**. Confirmation is a determination made _during_ the lifecycle, not a separate stage — an incident can be contained and recovered while the breach determination is still `suspected`, and the determination is what gates the handoff.

```
incident lifecycle (security module):
   detected -> triaged -> contained -> eradicated -> recovered -> closed
                  |                                      |
                  | breach determination (orthogonal):   |
                  |   unassessed -> suspected -> CONFIRMED (or not_a_breach)
                  |__________________________|
                              |
                  handoff fires HERE: the unassessed/suspected -> CONFIRMED
                  transition emits the breach-confirmed event to privacy
```

Two orthogonal axes on one record:

- **Lifecycle axis** (`detected` / `triaged` / `contained` / `eradicated` / `recovered` / `closed`) — the NIST 800-61r3 operational state. Tracks the security response.
- **Breach-determination axis** (`unassessed` / `suspected` / `confirmed` / `not_a_breach`) — the legal-disclosure determination. This is the axis the handoff watches.

**The handoff fires on exactly one transition:** `breach_determination` moving to `confirmed`. That transition is a deliberate, audited, human action (a high-privilege "confirm breach" verb), and it is the **clock-start trigger** (see § 2). The lifecycle axis can be anywhere when this happens — an operator may confirm a breach while still in `contained`, because the legal clock runs independently of the technical recovery.

**Why two axes instead of folding "confirmed" into the lifecycle:** a single linear lifecycle would force either "you cannot confirm a breach until you have contained it" (wrong — Art. 33 awareness can precede containment) or "confirmed is a stage you pass through" (wrong — it is a sticky determination, not a transient state). Orthogonal axes model the real semantics: the response progresses while the determination is set once and is then immutable.

State-transition records are **append-only** (each transition is an inserted row, never an update-in-place) so that the path to `confirmed` — and the timestamp of confirmation — is forensically reconstructable. This mirrors the slice-036 append-only audit-log pattern.

### 2. Who owns the 72-hour clock

**Split ownership, single immutable trigger:**

- **The security module owns the trigger.** The `breach_determination -> confirmed` transition stamps an immutable `breach_confirmed_at TIMESTAMPTZ` on the security incident record. This timestamp is the legal "awareness" moment. It is write-once: set on the confirm transition, never updatable thereafter (enforced by the append-only transition record + an application-level guard; the row's `breach_confirmed_at` is `NULL` until confirm and a CHECK forbids it transitioning back to `NULL`).
- **The privacy module owns the clock + the deadline.** When the handoff event arrives, the privacy module creates a `privacy.breach_notifications` workflow record whose `notification_deadline = breach_confirmed_at + INTERVAL '72 hours'`. The deadline is **computed once from the immutable trigger** and stored, not recomputed on read (so a clock-skew or a later code change cannot move a legally-fixed deadline). The privacy module tracks deadline status (`pending` / `notified` / `overdue`), drives the operator's notification UI, and records dispatch.

**Why split and not unified:** the breach determination is a _security_ judgment (is this a breach?); the notification obligation is a _privacy_ judgment (whom must we tell, by when?). Folding the clock into the security module would pull GDPR/HIPAA notification logic into `security.*`, violating sibling isolation. Folding the determination into privacy would let the privacy module reach into the security response lifecycle. The split keeps each module owning what it is the system of record for, with one immutable timestamp crossing the seam.

**The clock start is the security `breach_confirmed_at`, not the privacy record's creation time** — deliberately. If the handoff event is delayed (queue lag, the privacy module being toggled off then on), the deadline still anchors to the legally-correct awareness moment, not to when the privacy workflow happened to materialize.

### 3. The notification-target register

Notification targets are **privacy primitives** (they exist to satisfy GDPR Art. 33/34 + HIPAA breach-rule obligations), so they live in `privacy.*`:

- **`privacy.notification_authorities`** — supervisory authorities (a tenant's lead DPA, sectoral regulators, HHS OCR for HIPAA). Tenant-scoped, RLS-protected, largely static per tenant (configured once, reused across breaches). Modeled as a register the operator maintains, not derived per-breach.
- **`privacy.breach_affected_subjects`** — the affected-data-subject set for a _specific_ breach notification. This is the high-cardinality entity (a breach can touch millions of subjects — Information-disclosure + DoS threats). The ADR specifies it as a **join/staging entity keyed by `(tenant_id, breach_notification_id, subject_ref)`** where `subject_ref` is a privacy-local data-subject reference, NOT a denormalized PII blob. The register is **scale-bounded by design**: the implementation slice MUST treat it as a potentially-millions-row table (batched ingest, paginated read, no unbounded `SELECT *`), and affected-subject notification (Art. 34) is a _separate_ deadline from authority notification (Art. 33's 72h) — the design keeps them as distinct workflow legs on the same breach record.

Both target entities are `privacy.*`, RLS-scoped on `tenant_id`, and never referenced by the security module — the security incident does not know who gets notified; it only knows a breach was confirmed.

### 4. The cross-module seam (OQ #7 B3)

The handoff crosses the security/privacy boundary as a **citation-shaped reference, not a foreign key into the security incident**:

1. On `breach_determination -> confirmed`, the security module writes a **breach-determination evidence record** to the append-only evidence ledger (a new `evidence_kind`, e.g. `security.breach_confirmation.v1`, capturing the determination, the confirming actor, the `breach_confirmed_at` timestamp, and a minimum-disclosure summary). This produces an immutable `evidence.id`.
2. The handoff event carries that **`evidence.id`** (plus `tenant_id` and `breach_confirmed_at`) across the seam — not the `security.incidents.id` row.
3. The `privacy.breach_notifications` record references **`evidence.id`** (B3-legal — privacy MAY reference `evidence.id`) and optionally `policy.id` (the governing breach-notification policy). It does **NOT** reference `security.incidents.id`, and it does **NOT** reference `controls.id` (P0-446-4).

**Why route through evidence and not a direct FK:** the B3 rule's spirit is that privacy never reaches into the security domain model. An `evidence.id` is the project's canonical cross-module citation currency (already how privacy `processing_activities` cite security posture). Routing the handoff through an evidence snapshot means (a) the breach record stays security-local and mutable-by-security-only; (b) the privacy side gets an immutable, point-in-time citation it can render in a notification artifact; (c) the seam is exactly the seam slice 180 already blessed — no new cross-module reference shape is invented.

**What crosses the seam:** `evidence.id`, `tenant_id`, `breach_confirmed_at`. **What stays security-local:** the full incident lifecycle, the response timeline, the technical detail, the `security.incidents.id`. **What stays privacy-local:** the notification workflow, the target register, the affected-subject set, the deadline tracking.

**Audit trail across the seam:** the security-side confirm writes an audit row with `subject_module='core'`; the privacy-side workflow creation + every notification-dispatch writes audit rows with `subject_module='privacy'` (slice 180's column, used verbatim). The full chain — who confirmed the breach, when, who was notified, when — is reconstructable across both modules from one audit-log query, attributed by `subject_module`.

### Thin schema sketch (entity shapes, NOT a migration)

> This is ADR content for the implementation slice to build from. It is **not** a migration and **not** DDL to apply (P0-446-1).

```
security.incidents                         (security.* schema, RLS on tenant_id)
  id                  uuid
  tenant_id           uuid
  lifecycle_state     text   -- detected|triaged|contained|eradicated|recovered|closed
  breach_determination text  -- unassessed|suspected|confirmed|not_a_breach
  breach_confirmed_at timestamptz NULL     -- write-once; set on ->confirmed; immutable
  confirmed_by        text   NULL          -- high-privilege actor; required when confirmed
  ...                                       -- response detail stays security-local

security.incident_transitions              (append-only; one row per transition)
  id, tenant_id, incident_id, from_state, to_state, axis, actor, occurred_at

privacy.breach_notifications               (privacy.* schema, RLS on tenant_id)
  id                  uuid
  tenant_id           uuid
  breach_evidence_id  uuid   -- references evidence.id  (B3-legal; NOT incidents.id, NOT controls.id)
  governing_policy_id uuid NULL -- references policy.id (B3-legal)
  notification_deadline timestamptz        -- = breach_confirmed_at + 72h; computed once
  art33_status        text   -- pending|notified|overdue  (authority notification)
  art34_status        text   -- pending|notified|overdue|not_required  (subject notification)

privacy.notification_authorities           (privacy.* schema, RLS on tenant_id; per-tenant register)
  id, tenant_id, authority_name, jurisdiction, contact, framework  -- gdpr|hipaa|...

privacy.breach_affected_subjects           (privacy.* schema, RLS; high-cardinality, scale-bounded)
  tenant_id, breach_notification_id, subject_ref, notified_at NULL
```

No `privacy.*` schema is created by this ADR (slice 180 P0-180-1 deferred the namespace to privacy v0; this ADR preserves that — the namespace + these tables land with the implementation slice).

---

## Decision

**Recommendation: ADOPT-DEFERRED — record this two-axis state machine (NIST 800-61r3 lifecycle x breach-determination), split clock ownership (security owns the immutable `breach_confirmed_at` trigger; privacy owns the computed 72-hour deadline + workflow), the `privacy.*` target register, and the evidence-citation seam (`evidence.id` crosses, not `incidents.id`/`controls.id`) as the breach-handoff design. Build it as phase-3 / privacy-v0 work, gated on maintainer approval of this ADR. Confidence: HIGH.**

Concretely:

1. **The security module gains a new `security.incidents` entity** with two orthogonal axes (lifecycle + breach-determination). It is NOT a risk-register status extension and NOT a reuse of the evidence ledger as the system of record — incidents arrive _as_ evidence, but the breach record is its own entity.
2. **The handoff fires on the `breach_determination -> confirmed` transition only** — a deliberate, audited, high-privilege human action; never automatic (P0-446-5).
3. **Clock ownership splits:** security stamps the immutable `breach_confirmed_at`; privacy computes + stores `breach_confirmed_at + 72h` once and tracks the deadline. The legal clock anchors to the security awareness moment, not the privacy record's creation.
4. **The notification-target register is privacy-local** (`privacy.notification_authorities` + `privacy.breach_affected_subjects`), RLS-scoped, with the affected-subject set explicitly scale-bounded.
5. **The seam crosses an `evidence.id`** (a `security.breach_confirmation.v1` evidence snapshot), honoring OQ #7 B3 in letter (no `controls.id`) and in spirit (no `incidents.id` reach-in). Audit attribution uses slice 180's `subject_module`.

### Recommended phase

This **confirms OQ #10's timing**: phase-3 / privacy-v0 work. The handoff design is now specified; the build is gated on (a) maintainer approval of this ADR and (b) the OQ #7 "privacy v0 ships when a prospect surfaces demand" trigger. This ADR does **not** advance that timing (P0-446-3).

### The single decision the maintainer must make

> **Approve recording this design — two-axis `security.incidents` state machine, split clock ownership (security trigger / privacy deadline), `privacy.*` target register, and the `evidence.id` citation seam — as the breach-disclosure handoff specification, to be implemented as phase-3 / privacy-v0 work gated on prospect demand?** Approving this does NOT file the implementation slices and does NOT change OQ #10's open status or the privacy-v0 ship timing; it settles the design so the eventual implementing engineer does not choose a state machine unilaterally.

### Confidence rationale

**High**, because the load-bearing facts are verified against the actual repo, not assumed:

- The "no product-side incident entity exists" claim is verified: no `security.incidents` table in `migrations/sql/`, no `internal/api/incident` package; incidents arrive as `pagerduty.incident_summary` evidence. So the security side being _new_ is a fact, not a guess.
- The NIST 800-61r3 lifecycle is the repo's own canonical vocabulary (slice 372 `docs/governance/incident-response.md`), so the state-machine vocabulary composes with existing project language rather than inventing one.
- The B3 seam shape is the verbatim OQ #7 resolution + slice 180 foundation; routing through `evidence.id` is the citation currency privacy `processing_activities` already use, so the seam is not a new invention.
- The `subject_module` audit column exists and defaults to `'core'` (slice 180, verified), so the cross-seam audit trail needs no new infrastructure.

The residual uncertainty is in the _workflow UX_ (how the operator drives notification dispatch, the Art. 34 subject-notification ergonomics, the multi-jurisdiction authority-selection flow) — which is exactly why the workflow stays **deferred** to the implementation slice and only the **handoff** is specified here.

---

## Threat model (decision-level, per slice 446 STRIDE forward-checklist)

This is a decision artifact (no runtime surface), so the STRIDE pass is the forward-looking checklist the implementation inherits.

- **S — Spoofing.** Breach-confirm and notification-dispatch are **high-privilege actions**, not ordinary-user actions. The design requires `confirmed_by` to be a privileged actor recorded on the confirm transition, and notification-sent marks are equally privileged. The implementation slice MUST gate both behind an explicit role (not the default operator role) and MUST NOT allow a connector / rule / AI assist to perform them.
- **T — Tampering.** `breach_confirmed_at` is **write-once / immutable** once the determination reaches `confirmed` (the legal clock's start). The append-only `incident_transitions` record makes the path to confirmation reconstructable; a CHECK forbids `breach_confirmed_at` reverting to `NULL`. The privacy deadline is **computed once from the immutable trigger and stored**, so it cannot drift.
- **R — Repudiation.** Every transition (lifecycle + determination) and every notification dispatch writes an audit row, attributed by slice 180's `subject_module` (`'core'` security-side, `'privacy'` privacy-side). A **missed deadline cannot be retroactively hidden**: the `overdue` status is computed from the immutable `notification_deadline`, and the transition history is append-only. The full chain is one audit-log query across the seam.
- **I — Information disclosure.** All new entities (`security.incidents`, `privacy.breach_notifications`, the target register, the affected-subject set) are **RLS-scoped on `tenant_id`** (invariant #6), inheriting the four-policy split. The seam crosses only `evidence.id` + `tenant_id` + `breach_confirmed_at` — **not** the full incident detail or PII. The `security.breach_confirmation.v1` evidence snapshot is **minimum-disclosure** (determination + actor + timestamp + summary, not the raw incident dossier). Affected-subject records hold a `subject_ref`, not a denormalized PII blob.
- **D — Denial of service.** The affected-subject register is **explicitly scale-bounded** (a breach can touch millions of subjects): the implementation slice treats `privacy.breach_affected_subjects` as a high-cardinality table with batched ingest, paginated reads, and no unbounded scans. Noted here so the implementation does not discover it at load.
- **E — Elevation of privilege.** Breach-confirmation is the gate that starts a legal clock; it is **human-initiated, audited, and never automatic** (P0-446-5). No rule engine, no connector, no AI assist confirms a breach. The design has no automatic `suspected -> confirmed` path.

---

## Consequences

**Positive:**

- The eventual implementing engineer inherits a **settled state machine + seam** rather than choosing one unilaterally — the slice-400 / slice-455 "spike before build" discipline applied to the breach workflow.
- The design **honors sibling-module isolation by construction**: security owns the breach record, privacy owns the notification workflow, and the only thing crossing the seam is the project's existing `evidence.id` citation currency.
- The legal clock is **anchored to the immutable awareness moment**, not to incidental privacy-record timing — the GDPR Art. 33 deadline is forensically defensible.
- **No new audit infrastructure** is needed — slice 180's `subject_module` carries the cross-seam trail.
- The affected-subject **scale bound is named at design time**, not discovered at load.

**Negative / accepted trade-offs:**

- **Two orthogonal axes on the incident record are more complex than a single linear lifecycle.** Accepted: the real semantics (response progresses while determination is set once) genuinely require it; a linear model would mis-state either the awareness timing or the stickiness of confirmation.
- **The handoff routes through an evidence snapshot rather than a direct FK** — one indirection more than a foreign key. Accepted: it is the price of honoring B3's spirit (privacy never reaches into the security model), and the evidence ledger is the canonical cross-module citation seam anyway.
- **The security module grows a genuinely new entity** (`security.incidents`) rather than reusing the risk register or the evidence ledger as the breach system-of-record. Accepted: the risk register's lifecycle is treatment-status, not incident-lifecycle, and evidence is the _input_ to a breach determination, not the determination itself.

### Revisit list (open at implementation time, deliberately deferred here)

These are **not** decided by this ADR — they are the workflow-design calls the implementation slice owns:

1. **Notification UX** — how the operator drives Art. 33 authority notification + Art. 34 subject notification; the multi-jurisdiction authority-selection flow.
2. **HIPAA-specific timing** — the HIPAA breach rule's 60-day individual-notification + annual-vs-immediate HHS-notification branching (distinct from GDPR's 72h); the design accommodates it as a second deadline leg but does not specify the HIPAA workflow.
3. **AI-assist for notification drafting** — IF a future slice drafts notification text with AI, it falls under the CLAUDE.md AI-assist boundary (mandatory citations, human approval, `ai_assisted=true ↔ human_approver` guard). Out of scope here.
4. **Cross-border / multi-DPA** — the lead-DPA selection + one-stop-shop mechanism for multi-jurisdiction breaches.
5. **Whether `security.incidents` subsumes the slice-372 maintainer IR runbook** — NO for now (they are different surfaces per § Scope precision), but the implementation slice should confirm the boundary stays clean.

### Follow-on implementation-slice breakdown (filed ONLY after maintainer approves this ADR)

Per slice 446 AC-5, the discrete slices that would implement the workflow, gated on this ADR's approval:

| Slice (future)                                                                                                                                   | Ready/not-ready gate                                             |
| ------------------------------------------------------------------------------------------------------------------------------------------------ | ---------------------------------------------------------------- |
| **Breach state-machine + `security.incidents` entity** (migration + two-axis lifecycle + append-only transitions + confirm verb)                 | not-ready until ADR approved; first in dependency order          |
| **72h-clock + `privacy.breach_notifications`** (privacy schema namespace + deadline computation + status tracking)                               | not-ready; depends on the security entity + privacy-v0 namespace |
| **Notification-target register** (`privacy.notification_authorities` + `privacy.breach_affected_subjects`, scale-bounded)                        | not-ready; depends on the privacy namespace                      |
| **Security↔privacy handoff wiring** (`security.breach_confirmation.v1` evidence_kind + the confirm-emits-event seam + cross-module audit trail) | not-ready; depends on all three above                            |

None of these are filed by slice 446. They land after maintainer approval + the OQ #7 privacy-v0 demand trigger.

---

## Cross-references

- **[OQ #7 + #10](../../Plans/canvas/11-open-questions.md)** — the privacy sibling-module resolution (#7, incl. the breach-handoff sketch this ADR formalizes) + the disclosure/breach scope deferral (#10, which stays open).
- **Slice 180** (`docs/issues/180-privacy-module-foundation.md`) — the privacy foundation this ADR builds the handoff on: the `subject_module` audit column, the `module:<name>:enabled` flag pattern, the B1-B4 sibling discipline.
- **Slice 372** (`docs/governance/incident-response.md`) — the project's NIST SP 800-61r3 IR plan; the lifecycle vocabulary this ADR borrows (a _different_ surface — § Scope precision).
- **[ADR-0011](0011-rls-tenant-isolation.md)** — the RLS tenant-isolation pattern every new `security.*` / `privacy.*` breach entity inherits.
- **[ADR-0012](0012-append-only-evidence-ledger.md)** — the append-only evidence ledger the breach-confirmation snapshot writes to; the seam's `evidence.id` currency.
- **Slice 019** (`docs/issues/019-risk-register-crud.md`) — the risk register, confirmed NOT to be the breach record's home (treatment-status, not incident-lifecycle).
- **Slice 400 / [ADR-0010](0010-oscal-cosign-signing.md) + Slice 455 / [ADR-0016](0016-oidc-identity-for-keyless-signing.md)** — the "decision spike + ADR before build" shape this ADR mirrors.
- **`Plans/canvas/06-risk.md`** — incident/risk linkage (the security-side incident's design neighborhood).
