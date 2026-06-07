# 491 — HRIS connectors (Rippling + BambooHR) — joiner/mover/leaver evidence

**Cluster:** Connectors
**Estimate:** M (1-2d)
**Type:** JUDGMENT (evidence-kind shape + scope-minimum + stable-field choices)
**Status:** `ready`

## Narrative

The v1 connector roster ships the 7 MVP connectors (`aws`, `github`, `okta`,
`1password`, `osquery`, `jira`, `manual`; canvas §10.1, `connectors/`); the
planned layout (`CLAUDE.md`, "Planned repository layout") names `rippling` and
`bamboohr` in the HRIS tier. For the platform's persona — a SaaS startup security
leader — **the HRIS is the authoritative source of the employee roster, which is
the spine of the access-review and offboarding controls**: SOC 2 CC6.1/CC6.2/
CC6.3 ("access is granted on a need basis and revoked on termination") and the
access-review cadence (slice 374) both depend on a trustworthy joiner/mover/leaver
record. Today "prove every terminated employee's access was revoked" requires
manually reconciling the HRIS against IdP/app rosters. The HRIS connector brings
the authoritative roster + termination events into the evidence ledger so that
reconciliation becomes a control evaluation, not a spreadsheet.

This slice clusters **two closely-related HRIS connectors** — Rippling and
BambooHR — because they answer the same control question (the employee roster +
joiner/leaver events) and share an identical worker-lifecycle evidence shape, so
one slice keeps each a tracer-bullet while proving the HRIS evidence-kind family.
Both follow the slice-004 / 442 connector template (stable `actor_id`, stable
optional fields, `observed_at` granularity, register-per-run, scope minimums,
vendor-native auth; `feedback_connector_patterns`).

- **`connectors/rippling/`** collects **employee roster + employment-status**
  evidence (worker id, employment status, hire date, termination date,
  department — the lifecycle facts) via the read-only Rippling API.
- **`connectors/bamboohr/`** collects the same **employee roster +
  employment-status** evidence via the read-only BambooHR API.

Both register `profiles_supported` per run and `Push` each record to the single
`IngestEvidence` API.

**Scope discipline.** **Two connectors, one evidence surface each** (worker
lifecycle / employment-status roster), the minimum that demonstrates the HRIS
evidence family is a real first-class peer set. **HRIS holds the most sensitive
PII the platform will ever touch** (SSN, compensation, home address, bank
details, performance, health/benefits) — the connector collects ONLY the
lifecycle facts the access-review control needs and **explicitly excludes all
compensation / SSN / address / benefits / performance fields**. Neither ships
payroll evidence, neither ships a webhook/event-driven profile (pull-profile only
in v0 — honest interval), and neither changes the platform-side wire (push-only —
invariant #3). **Follow-on slices:** manager-hierarchy evidence for
review-routing; event-driven profile via HRIS termination webhooks (the
highest-value upgrade — real-time leaver signal for deprovisioning).

## Threat model (STRIDE) — connector family (source-credential heavy)

Each connector is a separate process holding **source-side credentials** (a
Rippling API key / a BambooHR API key). The dominant risks are credential
handling (over-broad HRIS scope, key leakage), **over-collection of extremely
sensitive employee PII** (the primary risk for HRIS), and keeping the platform
wire push-only.

**S — Spoofing.** Each connector authenticates TO the platform via its push
credential (the existing connector auth — OAuth client_credentials per slice 191) and TO its HRIS via the vendor API key. Risk: a stolen push credential, or
an HRIS key with broad scope (full PII / write).
**Mitigation:** push reuses the existing connector credential boundary; HRIS auth
uses an API key scoped **read-only** to the minimal employee-directory fields
(roster + employment status), documented as the required minimum. Keys stay
source-side; the platform never sees them (invariant #3).

**T — Tampering.** Evidence records carry a sha256 content-hash.
**Mitigation:** each pushed record is content-hashed (v1 evidence-integrity
primitive); ingest validates the hash. The connectors do not accept inbound
data — they only read the HRIS + push.

**R — Repudiation.** Which connector run produced which evidence must be
traceable.
**Mitigation:** register-per-run records the connector identity + run; each
record carries a stable `actor_id` (the rippling/bamboohr connector + run
context) and `observed_at` at a documented granularity (slice 004 pattern).

**I — Information disclosure (PRIMARY — load-bearing for HRIS).** The HRIS holds
the most sensitive PII in the customer's stack: SSN/national id, compensation,
home address, bank/payroll details, benefits/health enrollment, performance.
Risk: the connector over-collects any of these into the append-only evidence
ledger, where it would be impossible to un-write.
**Mitigation:** the connector collects ONLY worker id, employment status, hire
date, termination date, and department/team — the access-review lifecycle facts.
It **explicitly excludes** SSN/national id, compensation, home address, bank/
payroll, benefits/health, and performance fields — these are never read into an
evidence record. Worker identity is the stable worker id (+ work email where the
access-review join requires it), NOT the full PII profile. A test asserts no
excluded-PII field ever enters an evidence record. Keys are never logged. (This
discipline composes with the data-retention / disposal policy, slice 375, and
the append-only-ledger invariant #2 — what enters the ledger cannot be quietly
removed, so the gate must be at ingestion.)

**D — Denial of service.** A large org (thousands of workers + historical
records) could make a run unbounded.
**Mitigation:** paginated HRIS-API reads with bounded page sizes + a per-run cap;
pull on a named interval (honest, not "continuous"); run timeout.

**E — Elevation of privilege.** Risk: the HRIS key granted full-PII / write
scope "to be safe."
**Mitigation:** read-only minimal-field scope only; docs name the exact minimal
scope per HRIS and warn against full-PII / write keys. No platform-side privilege
beyond push (invariant #3).

## Acceptance criteria

**Connectors — collection**

- [ ] **AC-1.** `connectors/rippling/` and `connectors/bamboohr/` connectors
      land, each following the slice-004 / 442 template (register-per-run, stable
      `actor_id`, `observed_at` granularity, scope minimums).
- [ ] **AC-2.** Rippling collects **employee roster + employment-status**
      evidence (worker id, status, hire/termination date, department) via the
      read-only Rippling API.
- [ ] **AC-3.** BambooHR collects the same **employee roster +
      employment-status** evidence via the read-only BambooHR API.
- [ ] **AC-4.** Each authenticates via a read-only minimal-field HRIS API key,
      documented as the minimum, with an explicit warning against full-PII / write
      keys.

**Connectors — push**

- [ ] **AC-5.** Each collected record is pushed to the single `IngestEvidence`
      (`Push`) API — no platform-side wire change (invariant #3).
- [ ] **AC-6.** Each record carries a sha256 content-hash + stable optional
      fields.
- [ ] **AC-7.** Each connector registers `profiles_supported` (`pull` in v0) per
      run; the pull interval is named honestly.

**Evidence schema**

- [ ] **AC-8.** A shared worker-lifecycle evidence_kind (or one per connector)
      lands in the schema-registry schemas tree with `x-default-scf-anchors` set
      (OQ #9). The schema field set deliberately excludes the sensitive-PII
      fields named in threat-model I.

**Tests**

- [ ] **AC-9.** Per-connector unit/integration tests cover collect → push
      against a mocked HRIS API (no live Rippling/BambooHR in CI).
- [ ] **AC-10.** A test asserts neither connector emits SSN / compensation /
      home address / bank/payroll / benefits/health / performance fields —
      lifecycle facts only (threat-model I). This is the load-bearing PII guard.
- [ ] **AC-11.** A test asserts neither connector logs its HRIS API key.

**Docs / JUDGMENT artifact**

- [ ] **AC-12.** Each connector README documents the minimal read-only field
      scope (with the full-PII / write-scope warning), the pull interval, and the
      evidence kinds.
- [ ] **AC-13.** A decisions log
      (`docs/audit-log/491-hris-connectors-decisions.md`) records the
      shared-vs-per-connector evidence-kind choice, the included-vs-excluded
      field set (the PII boundary), the worker-identity choice (worker id ± work
      email), `x-default-scf-anchors`, scope-minimum, and stable-field JUDGMENT
      calls.
- [ ] **AC-14.** A changelog entry.

## Constitutional invariants honored

- **#3 — Single canonical inbound API (`IngestEvidence` / `Push`).** Both are
  first-class peer connectors holding source-side credentials; push-only wire.
- **#2 — Append-only evidence ledger.** Because the ledger cannot be quietly
  un-written, the PII-minimization gate is enforced at ingestion (the connector),
  not after.
- **Licensing — no closed proprietary connectors.** OSS, in-tree, read-only API.
- **Evidence integrity.** sha256 content-hash per record (v1 primitive).
- **Anti-pattern: honest intervals.** Each pull profile names its interval.

## Canvas references

- `Plans/canvas/04-evidence-engine.md` §4.1 — Evidence SDK, connectors,
  `profiles_supported`, push wire; §4.3 — append-only ledger (PII enters once).
- `CLAUDE.md` "Planned repository layout" — `connectors/rippling/`,
  `connectors/bamboohr/` named.
- `docs/issues/374-*` (access-review cadence) + `docs/issues/375-*` (data
  retention / disposal) — the controls this roster evidence serves and the
  retention discipline it composes with.
- `Plans/EVIDENCE_SDK.md` — full SDK contract incl. push profile.

## Dependencies

- **#003** (Evidence SDK proto + push client + CLI) — `merged`. The push surface.
- **#004** (AWS connector exemplar) — `merged`. The connector pattern template.
- **#191** (SDK OAuth client_credentials migration) — `merged`. Connector push
  credential.

## Anti-criteria (P0 — block merge)

- **P0-491-1.** Does NOT widen the platform-side wire — push only (invariant #3).
- **P0-491-2.** Does NOT require or document full-PII / write HRIS scope —
  read-only minimal-field least-privilege only (threat-model E).
- **P0-491-3.** Does NOT collect SSN / compensation / home address / bank/payroll
  / benefits/health / performance fields — lifecycle facts only (threat-model I).
  This is the P0 PII guard.
- **P0-491-4.** Does NOT log or transmit an HRIS API key into the platform.
- **P0-491-5.** Does NOT ship a closed/proprietary collector (licensing).
- **P0-491-6.** Does NOT label a pull profile "continuous monitoring."
- **P0-491-7.** Does NOT implement payroll / manager-hierarchy / event-driven
  evidence — follow-ons.

## Skill mix (3-5)

`grill-with-docs` · `tdd` (collect→push round-trip; mocked HRIS APIs) ·
`security-review` (source-credential + sensitive-PII over-collection — the
load-bearing review for this slice) · `simplify` · `changelog-generator`.

## Notes for the implementing agent

- **Phase-2 grill output:** both connectors are the slice-004 / 442 pattern;
  cluster them because the worker-lifecycle evidence shape is identical. The
  defining risk — above every other connector in this batch — is sensitive-PII
  over-collection into an append-only ledger. The included-vs-excluded field set
  is THE load-bearing design decision; test the exclusion explicitly (AC-10).
- **JUDGMENT call: shared vs per-connector evidence_kind.** Prefer one shared
  `hris.worker_lifecycle.v1` shape if the lifecycle field set is genuinely
  identical across Rippling + BambooHR; split only if the vendor models diverge
  enough to make a shared shape lossy. Record the call.
- **JUDGMENT call: worker identity.** Decide whether work email is needed for the
  access-review join (likely yes) and document that it is the only contact field
  collected; the stable key is the worker id. Record the call.
- **Other JUDGMENT calls you own:** the precise lifecycle field set,
  `x-default-scf-anchors`, scope minimum per HRIS. Record in the decisions log;
  the maintainer re-checks anchor accuracy (OQ #9 load-bearing).
- Reuse `feedback_connector_patterns` conventions across both connectors.
- Detection-tier: `none` unless a bug surfaces during the build.
