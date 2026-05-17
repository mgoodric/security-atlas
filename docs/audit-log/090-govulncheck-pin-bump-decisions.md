# Decisions log — Slice 090 (govulncheck pin bump for Go 1.26 compat)

This is an AFK slice (per `Plans/prompts/04-per-slice-template.md` "Slice types"). The slice is a one-line workflow YAML edit + this decisions log; the only meaningful judgment call is which version to bump to.

## Build-time judgment calls

### D1 — Bump to `v1.1.4` (the only newer release available on `golang/vuln`) (HIGH confidence)

**Decision:** bump `.github/workflows/ci.yml` govulncheck pin from `@v1.1.3` to `@v1.1.4`.

**Rationale:** `golang.org/x/vuln/cmd/govulncheck` is published from `github.com/golang/vuln`. Released versions on that repo (as of 2026-05-16):

| Tag             | Released                   |
| --------------- | -------------------------- |
| v1.0.0 — v1.0.4 | 2024-03-20                 |
| v1.1.0 — v1.1.3 | 2024-04 → 2024-07          |
| **v1.1.4**      | 2025-01-13 (newest stable) |

There is no v1.2.x or beyond — the slice doc's speculative "candidate version v1.2.0" predates a check against the actual release history. **v1.1.4 is the only newer pin available.** The bump is mechanical: one tag forward.

**Alternatives considered:**

- `@latest`: explicitly rejected by slice 089 D2 ("pinned versions, no `@latest`" — floating pin creates the class of failure where a green PR becomes a red PR with no in-repo change).
- Pin to a specific commit SHA: more reproducible than a tag, but introduces friction without obvious benefit. v1.1.4 is the latest stable tag; that's the right granularity.
- Stay on v1.1.3 and pin a Go version on the install step that's compatible: introduces a divergence between CI runner Go (1.26) and the install-step Go (<1.24), which is invasive workflow churn for a temporary workaround.
- Skip govulncheck entirely until upstream ships v1.2.x: drops the security signal indefinitely; loses the Q2 audit's intent.

### D2 — Verify via CI on this slice's own PR (HIGH confidence)

**Decision:** the v1.1.4 install + scan outcome is verified by observing this slice's own PR's `Go · govulncheck` job. Three valid outcomes per slice 090 AC-3:

- **(a) Green** — no reachable HIGH/CRITICAL vulns; pass.
- **(b) Red with findings** — govulncheck reports specific CVEs reachable from the codebase; engineer's grill picks per-finding (bump dep / suppress with justification / escalate). Slice 089's AC-8 procedure applies.
- **(c) Red with new install error** — v1.1.4 ALSO fails to install under Go 1.26; different incompatibility. File a follow-on slice 095; do NOT loop on pin-bumping in this slice.

This decision will be appended (post-CI observation) with the actual outcome and any per-finding choices.

### D3 — Slice 089 decisions log AC-8 entry already corrected (LOW judgment, HIGH confidence)

**Decision:** slice 089's AC-8 entry was corrected in slice 090's original PR (#179, commit `734d731`) — that work was done at slice-090-FILING time. This iteration only needs to land the pin-bump itself; the slice-089 correction note already points at slice 090 generically. No further edits to slice 089's decisions log are needed.

### D4 — Suppression mechanism: NOT shipped in this slice (HIGH confidence)

**Decision:** do NOT ship a `govulncheck-ignore` file, `.audit-ci.json`, or any other suppression mechanism in this slice. Per slice 089 D2: "If the first-run cleanliness step (AC-8) surfaces a CVE that genuinely cannot be remediated in this PR, add the suppression file in the same PR with a comment containing: CVE-ID + reason for ignore + revisit-date or revisit-condition (per P0-A2)."

**Rationale:** suppression is per-finding, not preventative. Ship the bump first; if v1.1.4 surfaces real CVEs, the per-finding pick (D2 outcome b) decides whether suppression is needed.

## Acceptance criteria status

- [x] AC-1: `.github/workflows/ci.yml` `Go · govulncheck` job's install step bumped `@v1.1.3` → `@v1.1.4`. Inline comment cites slice 090.
- [ ] AC-2: New pin successfully installs on a fresh runner — verified at PR open.
- [ ] AC-3: New pin runs the actual scan and reports a meaningful result — verified at PR open.
- [x] AC-4: This decisions log records (1) chosen pin version + rationale, (2) install + scan outcome (post-CI append), (3) future "do not use" recommendation if any.
- [x] AC-5: Slice 089's AC-8 entry was already corrected in slice 090's original filing PR (#179, commit `734d731`); no further edit needed in this iteration.
- [x] AC-6: `docs/audits/2026-Q2-security-audit.md` MEDIUM finding's "Remediation status" line stays pointing at `9baeb7d` (slice 089's merge commit). This slice 090 is a follow-up fix; the audit-remediation campaign rollup is already complete.
- [ ] AC-7: Pre-commit clean. CI green on required-checks. The `Go · govulncheck` job is still informational-only (not added to required-checks — promotion is a future slice).

## Revisit-once-in-use list

- **D1 (v1.1.4):** when upstream `golang/vuln` ships v1.2.x or a security patch, bump again via a fresh slice. Recurring concern; not slice-specific.
- **D2 (post-CI outcome):** this decisions log is incomplete until the slice's own PR's CI run is observed. Append with the actual outcome at PR-open time.

## Confidence summary

3 of 4 decisions HIGH confidence. D2 is HIGH for the methodology but the actual outcome (a/b/c) is observable only after CI runs.
