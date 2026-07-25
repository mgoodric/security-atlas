# 491 — HRIS connectors (Rippling + BambooHR): JUDGMENT decisions log

Slice type: JUDGMENT. This file records the subjective build-time calls (the
shared-vs-per-connector evidence-kind choice, the included-vs-excluded
worker-lifecycle field set — the PII boundary — the worker-identity choice, the
`x-default-scf-anchors`, the scope minimums, the per-HRIS least-privilege auth
scope, and the stable-field choices). It does NOT block merge — the maintainer
iterates post-deployment from the "Revisit once in use" list.

- detection_tier_actual: none
- detection_tier_target: none

(No product-behavior bug surfaced during the build. Two self-inflicted authoring
mistakes were caught at the green step and fixed in the same PR: (1) a Cyrillic
`С` accidentally typed into the `EnvCompanyDomain` constant name — caught at
`go build`/review and corrected to ASCII; (2) the BambooHR `mapStatus` treated
the BambooHR sentinel termination date `0000-00-00` as a real date and so
classified a no-term-date Inactive worker as `terminated` rather than `on_leave`
— caught by the `TestMapStatus` table case and fixed by treating `0000-00-00` as
empty. Both are build-time authoring errors, not defects in shipped behavior.)

## Decisions made

### D1 — ONE shared `hris.worker_lifecycle.v1` evidence kind across both connectors (THE clustering call)

- **Options considered:** (a) one shared `hris.worker_lifecycle.v1` kind with a
  `source_hris` discriminator; (b) two per-connector kinds
  (`rippling.worker_lifecycle.v1` + `bamboohr.worker_lifecycle.v1`); (c) reuse
  the existing `okta.user_lifecycle.v1` kind.
- **Chosen:** (a), one shared kind, built once in
  `connectors/hris/workerrecord`, normalized once in `connectors/hris/worker`.
- **Rationale:** the worker-lifecycle field set is genuinely identical at the
  HRIS altitude — stable worker id, employment status, start/end dates,
  title, department, the manager assignment id, and work email. The spec's grill
  output explicitly prefers a shared shape "if the lifecycle field set is
  genuinely identical … split only if the vendor models diverge enough to make a
  shared shape lossy" — it does not diverge; the vendors differ only in
  vocabulary (Rippling `employmentStatus` ACTIVE/TERMINATED; BambooHR
  `status` Active/Inactive + `terminationDate`), which the per-connector
  `mapStatus` normalizers absorb into one shared `EmploymentStatus`. This mirrors
  the slice-488 (Datadog/Grafana shared `monitoring.alert_config.v1`) and slice-
  490 (Jamf/Intune shared `endpoint.device_posture.v1`) two-connector
  precedents exactly. Option (c) was rejected: `okta.user_lifecycle.v1` is the
  IdP account-state surface (Okta status enum, `mfa_enrolled`, `login`) — a
  different source answering "is the IdP account on/off", whereas the HRIS is the
  authoritative _employment_ roster the IdP state is reconciled _against_.
  Folding them would destroy the per-source provenance and break the
  reconciliation the access-review control performs. The shared-package layout
  (`connectors/hris/{worker,workerrecord,idem}`) is the slice-490
  `connectors/mdm/` pattern.

### D2 — Included-vs-excluded field set: worker-lifecycle facts ONLY (THE load-bearing PII boundary, threat-model I / P0-491-3)

- **Decision — INCLUDED (the whole evidence shape):** `source_hris`,
  `worker_id`, `employment_status` (required); `start_date`, `end_date`,
  `title`, `department`, `manager_assignment_id`, `work_email` (stable
  optionals). That is the entire payload.
- **Decision — EXCLUDED (never enters an evidence record):** SSN / national id,
  compensation / salary / pay rate, home address, bank / payment details,
  benefits / health enrollment, performance-review fields, date of birth,
  personal phone, gender / ethnicity / protected-class data.
- **Rationale:** the HRIS holds the most sensitive PII the platform will ever
  touch, and the evidence ledger is append-only (invariant #2) — what enters
  cannot be quietly un-written, so the minimization gate MUST be at ingestion
  (the connector), not after. The access-review + deprovisioning controls (SOC 2
  CC6.1/6.2/6.3) need only "who is employed, in what role/department, since when,
  until when, reporting to whom, at what work email" — none of the excluded
  fields advance that control question.
- **Enforcement — structural, at three layers:**
  1. **Request side.** Each client requests ONLY the minimal field set:
     Rippling via a `fields=` selector (`workers.LifecycleFields`); BambooHR via
     a **custom report** scoped to `LifecycleFields` (deliberately NOT the
     `/employees/directory` endpoint, whose field set is fixed and cannot be
     narrowed, nor the full-employee endpoint). The sensitive PII is never
     returned over the wire.
  2. **Type side.** The vendor `apiEmployee` / `RawWorker` structs and the
     shared `worker.RawWorker` / `worker.Worker` structs have **no field** for
     any excluded value — a leak would be a compile error. `json.Decode` discards
     any unmatched JSON key, so even a misconfigured over-broad token cannot
     materialize excluded fields into connector memory.
  3. **Test side.** `integration_test.go:TestEmittedRecords_NoSensitivePII`
     (both connectors) asserts the emitted payload has only allow-listed keys and
     no banned substring; the `client_test.go` field-query assertions confirm the
     request never asks for a banned field; `worker_test.go:
TestRawWorker_HasNoSensitivePIIField` reflects over the structs and fails on
     any field name matching a sensitive-PII concept; and
     `TestCredential_NeverLogged` confirms the key is never logged (AC-10/AC-11).

### D3 — Worker identity: opaque worker id is the stable key; work email is the ONLY contact field

- **Decision:** the stable key is the HRIS-native `worker_id`. `work_email` is
  collected as an optional, and is the **only** contact field.
- **Rationale:** the access-review join keys the HRIS roster against IdP/app
  accounts, and IdP/app accounts are overwhelmingly keyed by work email — so the
  join genuinely needs it (the spec's grill output: "likely yes"). No personal
  email, personal phone, or any other contact field is collected. The manager
  link is an opaque `manager_assignment_id` (the manager's worker id), never the
  manager's contact detail — the same assignment-identity discipline as slice
  490's device→owner boundary.

### D4 — `x-default-scf-anchors` = `["IAC-22", "IAC-09", "HRS-04"]` (maintainer recheck — OQ #9, load-bearing)

- **Decision:** mirror the existing `okta.user_lifecycle.v1` anchors —
  `IAC-22` (Termination of employment / deprovisioning), `IAC-09` (Account
  management / provisioning), `HRS-04` (Personnel security).
- **Rationale:** HRIS worker-lifecycle answers the _same_ joiner/mover/leaver
  control question as the Okta lifecycle surface, and those three anchors are
  already validated members of the SCF seed (used by `okta.user_lifecycle.v1`
  and other connectors). Flagged for maintainer accuracy recheck per OQ #9; the
  anchors are a default hint, not a binding mapping.

### D5 — Scope minimum: `service` + required `--environment`; `Result = INCONCLUSIVE`

- **Decision:** every record carries exactly two scope dimensions — `service`
  (`rippling` / `bamboohr`) and the operator-supplied `--environment` (required,
  PreRunE-enforced) — and `Result = RESULT_INCONCLUSIVE`.
- **Rationale:** the slice-004/490 scope-minimum convention. The connector
  reports descriptive lifecycle facts; the platform evaluator owns the
  access-review pass/fail per `(control, scope)` by reconciling the roster
  against IdP/app entitlements — the connector never asserts a verdict.

### D6 — Stable optionals + hour-truncated `observed_at` + register-per-run

- **Decision:** optional fields are emitted only when the source supplied them
  (no empty-string / zero-date placeholders); dates are emitted as ISO `date`
  strings; `observed_at` is truncated to the UTC hour, which collapses
  same-worker re-runs within the hour into one ledger row via the shared
  `idem.WorkerLifecycleKey`; each `run` is preceded by a `register` declaring
  `profiles_supported = [pull]`. This is the slice-004 stable-optional +
  register-per-run convention verbatim.

### D7 — Per-HRIS least-privilege read-only auth (P0-491-2 / threat-model E)

- **Rippling:** API token scoped to the read-only employee-directory /
  worker-lifecycle field group only (`RIPPLING_API_TOKEN`, bearer). Never a
  full-PII read group or a write scope.
- **BambooHR:** API key (HTTP Basic username) belonging to a user whose role
  grants read-only worker-directory access only (`BAMBOOHR_API_KEY` +
  `BAMBOOHR_COMPANY_DOMAIN`). Never a role that can see compensation/SSN/bank/
  benefits/performance or edit employees.
- **Both:** the credential is read from the environment (never a flag, so never
  in shell history), redacted on every `String()`/`GoString()` path, and never
  logged or pushed (invariant #3). The `permissions` subcommand prints the exact
  documented minimum with the no-full-PII / no-write warning.

### D8 — Pull-only profile; honest interval (P0-491-6, P0-491-7)

- **Decision:** `profiles_supported = [pull]`; the `PullInterval` constant names
  "24h (recommended; operator-scheduled — NOT continuous monitoring)". No
  webhook/event-driven profile, no payroll evidence, no manager-hierarchy
  evidence — all explicit follow-ons.

## Spillover filed

- **571** — HRIS manager-hierarchy evidence (review-routing). Parent 491.
- **573** — HRIS event-driven (termination webhook) profile — real-time leaver
  signal for deprovisioning (the spec's flagged highest-value upgrade). Parent 491.

## Revisit once in use (maintainer iteration list)

- Recheck the `x-default-scf-anchors` accuracy (OQ #9, D4).
- v0 reads the first bounded page only (Rippling `limit`; BambooHR full report).
  For very large orgs, add cursor pagination + a per-run cap (threat-model D) —
  fold into the manager-hierarchy or webhook follow-on if/when org size demands.
- Confirm the Rippling `employmentStatus` and BambooHR `status` vocabularies
  against live tenants and extend `mapStatus` if a real value falls through to
  `unknown`.
