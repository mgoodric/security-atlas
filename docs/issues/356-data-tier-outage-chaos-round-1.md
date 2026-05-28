# 356 — Chaos experiment execution: data-tier outage chaos round 1

**Cluster:** Resilience
**Estimate:** 1.5d (two bundled experiments)
**Type:** JUDGMENT
**Status:** `ready` — **deferred to v2+** (execution slice)

## Narrative

Executes two chaos experiments designed in slice 335:

- **Experiment 3** — Postgres primary unavailable
- **Experiment 5** — atlas container restart mid-evidence-push

The designs live at `docs/audits/335-chaos-experiment-design.md`
§§Experiment 3, 5. This slice does NOT redesign — it executes both as
a bundled round because they share an injection mechanism (`docker
stop` / `restart`) and both verify error-shape discipline under
data-tier outage.

This slice is **deferred to v2+**. It was filed by slice 335 as a
spillover slot per AC-4. The bundle decision is captured in slice
335's decisions log Decision D3.

### Why v2+ and why bundled

Slice 335 was design-only. The bundle decision is per slice 335
Decision D3 — both experiments share `docker stop` / `restart` as
the injection mechanism and both verify the same class of claim
(error-shape under data-tier outage). One execution round, one
operator session, one decisions log entry per experiment.

### Hypotheses under test

(Pulled verbatim from slice 335 §§Experiment 3, 5 for executor
convenience.)

- **Exp 3:** When Postgres becomes unavailable, the platform returns
  structured 5xx responses (not stack traces) within 5 seconds; no
  request hangs.
- **Exp 5:** When the atlas container restarts during an active push,
  the SDK's retry-with-backoff (slice 003) re-sends the record; the
  idempotency key prevents duplicates; no evidence is lost.

## Threat model

Execution slice; injects controlled failures via `docker stop` /
`restart`. STRIDE pass:

- **S:** No auth surface for Exp 3 / Exp 5. CLEAN.
- **T:** Container stop / restart — bounded by docker-compose blast
  radius.
- **R:** Outcomes logged in
  `docs/audit-log/356-data-tier-outage-chaos-round-1-decisions.md`.
- **I:** Same as slice 335.
- **D:** **Load-bearing for both experiments.** These ARE the
  failure-injection events.
- **E:** Dev-level access.

## Acceptance criteria

### Experiment 3 (Postgres primary down)

- [ ] **AC-1.** Pre-execution checklist from slice 335 §Experiment 3
      satisfied.
- [ ] **AC-2.** Steady-state captured BEFORE: all endpoints 2xx,
      `/healthz` OK.
- [ ] **AC-3.** Postgres stopped via `docker-compose stop postgres`;
      synthetic requests fired for 60s; status codes, latencies,
      response body shapes captured.
- [ ] **AC-4.** Postgres restarted; recovery time measured.
- [ ] **AC-5.** Falsification check: no request hangs > 30s; atlas
      does not crash; no stack traces in response bodies.

### Experiment 5 (atlas restart mid-push)

- [ ] **AC-6.** Pre-execution checklist from slice 335 §Experiment 5
      satisfied (idempotency keys in use; fresh tenant scope).
- [ ] **AC-7.** SDK client pushes at 1/s for 60s; `docker-compose
  restart atlas` injected after 10s.
- [ ] **AC-8.** Final `evidence_records` row count for the test
      tenant equals 60 — NOT more (duplicates) and NOT less (lost).
- [ ] **AC-9.** All record IDs unique.

### Shared

- [ ] **AC-10.** Post-experiment report at
      `docs/audit-log/356-data-tier-outage-chaos-round-1-decisions.md`
      with per-experiment outcome.
- [ ] **AC-11.** Cross-references slice 335 (design), slice 003 (SDK
      retry contract verified by Exp 5).

## Anti-criteria

- **P0-1.** Does NOT target atlas-edge or hosted.
- **P0-2.** Does NOT introduce a chaos framework as a dependency.
- **P0-3.** Does NOT modify the SDK's retry-with-backoff
  configuration — the test verifies the existing contract.
- **P0-4.** Does NOT auto-merge.

## Dependencies

- **#335** (chaos experiment design) — `merged`. The design contract.
- **#003** (Evidence SDK push) — provides the retry contract verified
  by Exp 5.

## Notes for the implementing agent

1. Read `docs/audits/335-chaos-experiment-design.md` §§Experiment 3
   and Experiment 5 FIRST.
2. The two experiments can run in either order. Recommended:
   Exp 5 first (cleaner state — just push + restart), then
   Exp 3 (postgres stop affects everything).
3. Reset docker-compose between experiments: `docker-compose down -v
&& docker-compose up -d`.
4. If Exp 5's ledger count is OFF by even one, the SDK's
   idempotency contract is broken — file a high-severity follow-up
   immediately.
