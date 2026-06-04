# 350 — Branch-coverage floor for security-critical packages

**Cluster:** Quality / coverage
**Estimate:** 1.5d
**Type:** AFK
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Slice 333's QA strategy audit
(`docs/audits/333-qa-strategy-gap-analysis.md`) finding Q-4: the Go
coverage ratchet (`cmd/scripts/coverage-thresholds.json`) enforces
line-coverage floors. A 75% line-floor on `internal/api/oauth` is
satisfied by happy-path coverage — the dangerous error branches
(invalid grant, expired code, revoked token, tenant-switch denied,
super-admin escalation refused) can sit untested and the gate stays
green. Article IX (integration-first) helps because integration tests
hit error paths more naturally, but there is no explicit "this
package's BRANCH coverage matters more than its line coverage"
discipline.

This slice introduces a **security-critical branch-floor tier** as a
named subset of the existing ratchet contract. Floor is branch
coverage, not line coverage; target is ≥90%; the tier is small
(~10 packages).

### Proposed initial roster (subject to audit during implementation)

- `internal/auth/jwt`
- `internal/auth/keystore`
- `internal/auth/tokensign`
- `internal/api/oauth` (umbrella)
- `internal/api/oauth/oauthcode`
- `internal/api/oauth/oauthclient`
- `internal/api/oauth/revocation`
- `internal/api/oauth/userprefs`
- `internal/api/authzmw`
- `internal/tenancy`

This is the auth-substrate-v2 spine plus the tenancy plumbing — the
load-bearing security primitives where an error-path regression is
the worst outcome.

### What ships

1. **Tier definition.** Extend `coverage-thresholds.json` schema
   v2 → v3 with a `$security_critical_branch_floor` block: a separate
   threshold map keyed by package, with values being branch-coverage
   percentages.
2. **Branch coverage measurement.** `go test -cover` measures lines
   by default; `-covermode=count` is required to distinguish branches.
   Add the flag in CI for the security-critical-tier packages; emit a
   separate coverage profile.
3. **Gate.** Extend `cmd/scripts/coverage-gate` to read the new block
   and enforce branch floors alongside line floors.
4. **Initial floor measurement.** Run the measurement once; floor =
   `floor(measured - 2pp)` per the project's standard ratchet rule.
   Lift floors to 90% in the SAME PR for any package below it (write
   the missing branch tests — per ratchet contract, never lift the
   threshold without writing the tests).
5. **Documentation.** Update CLAUDE.md "Testing discipline" with the
   security-critical tier.

### Why this matters

The project will run customer due diligence against itself. Security
review questions like "show me your branch coverage on JWT signing"
are answerable today by line-coverage proxies; they should be
answerable by direct measurement. The cost is small (the package list
is short); the signal is high.

## Threat model

This slice surfaces gaps in branch coverage of security-critical
paths. STRIDE pass:

- **S/T/R/D/E:** No runtime changes; CI configuration + threshold
  data only.
- **I (information disclosure):** The measured branch-coverage gaps
  are a roadmap to untested security-critical paths. **Mitigation:**
  do NOT publish the per-branch gap report; only the per-package
  floor numbers. Same discipline as slice 327 / 329 / 333.

## Acceptance criteria

- [ ] **AC-1.** `coverage-thresholds.json` schema v3 lands with
      `$security_critical_branch_floor` block.
- [ ] **AC-2.** `cmd/scripts/coverage-gate` enforces both line and
      branch floors.
- [ ] **AC-3.** Initial roster (10 packages above) is enrolled with
      measured floors; floors set at `floor(measured - 2pp)`.
- [ ] **AC-4.** For any package in the roster below 90% branch
      coverage, ship the missing tests in the SAME PR; lift the floor
      to 90% in the SAME PR.
- [ ] **AC-5.** CLAUDE.md "Testing discipline" updated to document
      the new tier.
- [ ] **AC-6.** Cross-references slice 333 Q-4 and slice 069.
- [ ] **AC-7.** `pre-commit run --files` passes.

## Anti-criteria

- **P0-1.** Does NOT lower any existing line-coverage floor.
- **P0-2.** Does NOT extend the roster beyond auth-substrate-v2 +
  tenancy in this slice. Round-2 can add more if the value is proven.
- **P0-3.** Does NOT publish per-branch gap reports outside the
  repo (information-disclosure mitigation).
- **P0-4.** Does NOT set the floor at 100% — branch coverage on
  defensive code paths (`if err != nil` in init paths) is acceptable
  at <100%. 90% is the target.

## Dependencies

- **#333** (QA strategy audit) — `merged`. Defines Q-4.
- **#069** (verification suite) — `merged`. The ratchet contract.
- **#187** through **#192** (auth-substrate-v2 spine) — all merged.
  The security-critical packages are stable.

## Notes for the implementing agent

The mechanical work is small (~1 day). The judgment is in the
initial floor numbers: branches with documented defensive intent
(`// defense in depth — should not be reachable`) are OK to leave
uncovered; branches that represent actual error paths from
hostile input MUST be covered. Use judgment per branch; document
the judgment in a per-package note inline in the threshold file.
