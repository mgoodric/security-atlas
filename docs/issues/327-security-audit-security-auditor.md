# 327 — Security audit via voltagent-qa-sec:security-auditor

**Cluster:** Security
**Estimate:** 2d
**Type:** JUDGMENT
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

The v1 backlog is fully merged (69/69 v1 slices on `main`; canvas hub
`Plans/ARCHITECTURE_CANVAS.md`). Before the v1 binary success test —
"does the solo security leader run their next SOC 2 audit out of
security-atlas?" — we want an external-perspective security audit pass
via the `voltagent-qa-sec:security-auditor` agent. This slice runs the
agent against `main` and converts its findings into follow-up slices
via `/idea-to-slice`. The audit itself is read-only-with-findings;
fixes are downstream slices.

**Audit surface.** Full security pass across:

- **AuthN:** OIDC relying-party flows (slices 187-198) — token exchange,
  IdP impersonation, code-injection / replay surfaces, session fixation.
- **AuthZ:** OAuth Authorization Server JWT issuance — ES256 signing,
  claim validation, tenant-switch token-exchange (RFC 8693), revocation
  (RFC 7009), introspection (RFC 7662). `internal/auth/keystore`,
  `internal/auth/jwt`, `internal/auth/tokensign`, `internal/api/oauth`.
- **Tenant isolation:** Postgres Row-Level Security on every
  tenant-scoped table. Verify RLS-denies-on-missing-context invariant
  is enforced at the DB layer (not application code). Cross-reference
  `internal/db/integration_test.go` role model.
- **Evidence integrity:** sha256 content-hash per record + cosign
  signing of audit-export bundles. Append-only ledger invariant
  (`canvas §4.3`).
- **Secrets handling:** JWT keystore rotation, cosign private keys,
  IdP client secrets, connector credentials, DB connection strings.
- **AI-assist boundary schema enforcement:** the
  `ai_assisted=true ↔ human_approver` invariant — verify the schema
  constraint actually prevents the bypass (CLAUDE.md hard boundary).
- **OWASP top 10** across Go backend (`internal/**`, `cmd/**`) +
  TypeScript frontend (`web/**`) + Python (`oscal-bridge/**`).

**Why now:** the binary success test is "does the user not reach for
Vanta or a Google Sheet to fill a gap." A self-hosted GRC platform
whose own auth substrate has a high-severity vulnerability fails the
diligence-the-diligence-tool thesis before SOC 2 work even begins.

**Trigger:** Surfaced 2026-05-27 during the agent-driven audit-planning
session — one of 12 voltagent-qa-sec audit slices filed simultaneously
to cover the full QA + security surface before the v1 binary test.

**Disposition:** read-only audit + follow-up-slice fan-out.

## Threat model

This is an audit-only slice. The agent operates with developer-level
access against a demo-seed local environment. STRIDE pass on the audit
activity itself:

- **S (Spoofing):** Audit agent runs locally; no production identity
  involved. CLEAN.
- **T (Tampering):** Read-only — no code modifications during the audit
  phase. AC-1 enforces this.
- **R (Repudiation):** All findings logged in
  `docs/audit-log/327-security-audit-security-auditor-decisions.md`
  with severity + disposition; PR review trail provides repudiation
  resistance.
- **I (Information disclosure):** **Load-bearing.** Audit output could
  leak RLS predicates, evidence excerpts, key-rotation cadences, or
  attack vectors that are not safe for public follow-up slice content.
  Mitigation: each follow-up /idea-to-slice slice goes through the
  regular security pass at Phase 3 of that skill; the
  `327-...-decisions.md` log itself never includes raw tenant data
  (demo seed only — slice 205 dataset) and never includes
  fully-exploitable PoCs for unfixed vulnerabilities — only severity +
  one-line disposition + slot reference.
- **D (Denial of service):** Audit is run-once, not a continuous
  scanner. CLEAN.
- **E (Elevation of privilege):** **Load-bearing.** The audit agent
  operates with developer-level access; it MUST NOT assume elevated
  privileges (no admin DB user, no production credentials, no
  super-admin tokens). AC enforces this.

## Acceptance criteria

- [ ] **AC-1.** The `voltagent-qa-sec:security-auditor` agent runs
      against the security surfaces listed in the narrative, against
      a local `main` checkout with the demo-seed dataset (slice 205).
- [ ] **AC-2.** Each finding is recorded in
      `docs/audit-log/327-security-audit-security-auditor-decisions.md`
      with: short title · severity (Critical / High / Medium / Low /
      Informational) · one-line disposition · location reference
      (file/path or surface name) — but NO fully-exploitable PoCs for
      unfixed Critical/High findings (those go in a private channel to
      the maintainer).
- [ ] **AC-3.** For each High and Critical finding, a follow-up slice
      is filed via `/idea-to-slice` in the same session. The
      follow-up slice's slot number is appended to AC-2's decisions
      log entry so the maintainer can trace audit → follow-up.
- [ ] **AC-4.** Medium findings: file a single consolidated tracking
      slice OR file individually — the engineer's call, documented in
      the decisions log.
- [ ] **AC-5.** Low / Informational findings: documented in the
      decisions log only; no follow-up slice required.
- [ ] **AC-6.** No code is modified as part of this slice — audit is
      read-only-with-findings; fixes are downstream slices. CI diff
      for the slice's PR contains ONLY:
      `docs/issues/327-security-audit-security-auditor.md`,
      `docs/issues/_STATUS.md`, and
      `docs/audit-log/327-security-audit-security-auditor-decisions.md`.
- [ ] **AC-7.** The audit decisions log includes a "Surface coverage"
      section confirming each item from the narrative was visited
      (auth, OAuth AS, RLS, evidence integrity, secrets, AI-assist
      boundary, OWASP) — so a future audit can see what was actually
      examined vs claimed.
- [ ] **AC-8.** The audit agent operates with no elevated privileges
      — the run uses a local dev DB role, no production credentials,
      no super-admin token. Decisions log documents the role/identity
      used.
- [ ] **AC-9.** `pre-commit run --files
docs/issues/327-security-audit-security-auditor.md
docs/issues/_STATUS.md
docs/audit-log/327-security-audit-security-auditor-decisions.md`
      passes at PR-time.

## Constitutional invariants honored

- **Survive a third-party security review (canvas §6).** This slice
  pre-empts that review by running an internal one against the spec'd
  attack surface.
- **AI-assist boundary (CLAUDE.md).** The audit agent itself does NOT
  auto-publish findings to public channels; the maintainer reviews the
  decisions log + each follow-up slice's spec before merge.
- **Tenant isolation enforced at the DB layer (canvas §5.4 / invariant
  #6).** A primary audit target — verify RLS-denies-on-missing-context
  holds for every tenant-scoped table.
- **Evidence integrity (canvas §4.3).** sha256 content-hash + cosign
  signing must hold end-to-end; a primary audit target.

## Canvas references

- `Plans/canvas/04-evidence-engine.md` — evidence integrity expectations
- `Plans/canvas/05-scopes.md` §5.4 — RLS enforcement model
- `Plans/canvas/09-tech-stack.md` — OIDC RP + OAuth AS architecture
- `Plans/canvas/01-vision.md` §6 — survive third-party security review

## Dependencies

- **#069** (testing discipline) — `merged`. Provides the four-surface
  test base the audit agent reasons against.
- **#187-#198** (auth-substrate-v2 spine) — all `merged`. Auth surface
  is the primary audit target.
- **#205** (demo seed dataset) — `merged`. Audit runs against demo
  data only.

## Anti-criteria (P0 — block merge)

- **P0-327-1.** Does NOT bundle multiple audit findings into one slice
  — one finding = one follow-up `/idea-to-slice` slice. Tracer-bullet
  discipline is the whole point.
- **P0-327-2.** Does NOT auto-merge this slice's PR or any follow-up
  slice's PR. The maintainer reviews each spec before it enters the
  batch queue.
- **P0-327-3.** Does NOT run the audit agent against production tenant
  data. Demo seed only (slice 205 dataset).
- **P0-327-4.** Does NOT modify code as part of this slice's audit —
  audit is read-only-with-findings; fixes are downstream slices.
  Anti-criterion is enforced by AC-6's diff-shape constraint.
- **P0-327-5.** Does NOT include fully-exploitable PoCs for unfixed
  Critical / High findings in the public decisions log. Severity +
  disposition + location reference only; the maintainer receives the
  PoC privately.
- **P0-327-6.** Does NOT use elevated privileges (production DB
  credentials, super-admin tokens) for the audit run. Dev-DB role
  only; identity used is documented in the decisions log per AC-8.
- **P0-327-7.** Does NOT touch `CLAUDE.md`, canvas, mockups, or any
  code surface. Doc-only PR.

## Skill mix

- `voltagent-qa-sec:security-auditor` — the named audit agent
- `/idea-to-slice` — for filing each High/Critical follow-up slice
- Standard read/grep tooling — for surface enumeration before the
  agent runs

## Notes for the implementing agent

**Phase order:**

1. Bring up a local dev environment with the demo-seed dataset loaded
   (slice 205's `POST /v1/admin/demo/seed` against a fresh install).
2. Enumerate the surfaces from the narrative; capture the file paths
   you intend to feed to the audit agent.
3. Invoke `voltagent-qa-sec:security-auditor` with those surfaces +
   the threat-model context from this slice.
4. Triage findings into Critical / High / Medium / Low / Informational
   buckets.
5. For each High + Critical: invoke `/idea-to-slice` with the finding
   as the idea text + sufficient context for the implementing engineer
   to repro + fix. Record the resulting slot number in the decisions
   log.
6. For Medium findings: decide bundle-vs-individual based on whether
   they're a coherent thread (one slice for "all input-validation
   gaps") or independent (one slice each). Document the choice.
7. Low / Informational: decisions log only.
8. Write the audit decisions log with the structure: Decisions made ·
   Revisit once in use · Confidence per decision — per the
   per-slice-template's JUDGMENT slice convention.

**Severity rubric (use this, don't invent your own):**

- **Critical** — RCE, auth bypass, RLS bypass, secrets in repo, evidence
  ledger tampering reachable without admin role.
- **High** — privilege escalation requiring some prerequisite, sensitive
  data exposure across tenants, broken cryptographic primitive.
- **Medium** — info disclosure within tenant, DoS via unbounded input,
  missing security header on auth surface.
- **Low** — hardening recommendation, defense-in-depth gap, finding
  with no current exploit path.
- **Informational** — observation, future-work-suggestion, doc gap.

**Spillover discipline.** If the agent flags a finding outside the
seven narrative surfaces (e.g. an observability gap, a CI hardening
gap), file it as a separate slice — do NOT expand this slice's scope.
The next slot is the next free integer; recompute via
`ls docs/issues/[0-9]*.md | sed ... | sort -n | tail -1 | awk '+1'`
each time.

**Audit log filename:**
`docs/audit-log/327-security-audit-security-auditor-decisions.md`
