# 090 — Bump `govulncheck` pin for Go 1.26 toolchain compatibility

**Cluster:** Infra
**Estimate:** 0.25d
**Type:** AFK

## Narrative

Surfaced during slice 089 post-merge observation, captured as follow-up per continuous-batch policy.

Slice 089 (PR #177) pinned `govulncheck@v1.1.3` per its decisions log §D3 (current stable as of 2026-05-15). The decisions log's AC-8 entry stated the first run was "cancelled by GH concurrency on the status-flip commit before completing" and that the second-run outcome would be appended if a CVE surfaced.

**Re-read of the actual second-run logs after merge:** the job FAILED, but **not** because govulncheck found a reachable CVE. It failed because govulncheck v1.1.3 cannot compile under the runner's Go toolchain. The failure surfaces during the `go install golang.org/x/vuln/cmd/govulncheck@v1.1.3` step itself:

```
# golang.org/x/tools/internal/tokeninternal
../../../go/pkg/mod/golang.org/x/tools@v0.23.0/internal/tokeninternal/tokeninternal.go:64:9:
invalid array length -delta * delta (constant -256 of type int64)
```

`govulncheck v1.1.3` transitively depends on `golang.org/x/tools v0.23.0`, which contains a constant-folding pattern that newer Go versions reject. CI runner runs Go 1.26 (per all five `actions/setup-go@v6` blocks in `.github/workflows/ci.yml` at lines 105 / 195 / 330 / 427 / 734).

**The govulncheck CI job is silently broken on main.** It exits non-zero from the install step before it ever evaluates the project's modules. Every PR sees a red `Go · govulncheck` check, but the failure is meaningless — no actual scanning is happening. This is the worst possible outcome for a security tool: it appears to be running, conveys no information, and conditions reviewers to ignore its red signal.

**Fix:** bump the govulncheck pin to a newer release that pins newer `x/tools`. Engineer's grill picks the exact version at run-time; reasonable candidates as of authoring this slice:

- `golang.org/x/vuln/cmd/govulncheck@v1.2.0` (newer minor)
- `golang.org/x/vuln/cmd/govulncheck@latest` — explicitly rejected by slice 089's D2 anti-pattern (floating pin = flake source); pick a specific tag

The fix is a single-line edit to `.github/workflows/ci.yml`. The risk surface is the new pin: it must (a) install cleanly under Go 1.26, (b) successfully scan our project, and (c) report a real signal (either green = no reachable vulns, or red = identified reachable vuln with details).

## Acceptance criteria

- [ ] AC-1: `.github/workflows/ci.yml` `Go · govulncheck` job's install step bumps the pin from `@v1.1.3` to a version that successfully compiles under Go 1.26. Inline comment cites slice 090.
- [ ] AC-2: The new pin successfully installs on a fresh runner — verified by the slice's own PR's `Go · govulncheck` job reaching the actual `govulncheck ./...` evaluation step (not the install step) without errors.
- [ ] AC-3: The new pin runs the actual scan and reports a meaningful result. Three valid outcomes:
  - (a) **Green** — no reachable HIGH/CRITICAL vulns; the job passes.
  - (b) **Red with findings** — govulncheck reports specific CVEs reachable from the codebase; the engineer's grill picks per-finding fix (bump dep / suppress with justification / escalate). Same shape as 089 AC-8's first-run procedure.
  - (c) **Red with new install error** — different incompatibility surfaces. Engineer's grill decides between further bump, downgrade, or escalation. Record in decisions log.
- [ ] AC-4: `docs/audit-log/090-govulncheck-pin-bump-decisions.md` records: (1) chosen pin version + rationale, (2) install + scan output summary (green/findings/install-error), (3) whether 089's broken `@v1.1.3` pin should be added to a "do not use" list in future tooling docs.
- [ ] AC-5: `docs/audit-log/089-dependency-vulnerability-scanning-decisions.md` AC-8 entry gets an appended correction note ("post-merge observation: govulncheck install failed under Go 1.26 toolchain; pin bumped in slice 090"). Edit the existing AC-8 section rather than overwriting the original observation.
- [ ] AC-6: `docs/audits/2026-Q2-security-audit.md` MEDIUM finding's "Remediation status" line for slice 089 stays pointing at `9baeb7d` (slice 089's merge commit) — this slice 090 is a follow-up fix, not a re-do of 089. Optionally append "+ govulncheck pin bump (#090) for Go 1.26 toolchain compat" to clarify the chain.
- [ ] AC-7: Pre-commit clean. CI green on required-checks. The `Go · govulncheck` job is still informational-only (not added to required-checks — that promotion is a separate future slice once the maintainer has observed cadence + false-positive rate).

## Constitutional invariants honored

- **Working norms — Surgical fixes**: smallest viable change. One workflow-line edit + decisions log + correction note on the 089 decisions log. No broader CI restructuring.
- **AI-assist boundary**: nothing AI-generated. Workflow YAML + small markdown.
- **Pinned-versions discipline (per slice 089 D2)**: stick to specific `@vN.M.P` tags, NOT `@latest`. Floating pins violate slice 050 AC-1.

## Canvas references

- _(none — operational tool-pin hygiene; canvas doesn't speak to govulncheck version pinning)_

## Dependencies

- **089** (merged at `9baeb7d` / PR #177) — slice 090 fixes the pin slice 089 introduced

## Anti-criteria (P0 — block merge)

- **P0-A1**: Does NOT replace `govulncheck` with another tool (e.g., `nancy`, Snyk, GitHub Advanced Security's Dependabot graph alerts). Slice 089 chose govulncheck deliberately for its call-graph filter; this slice fixes the chosen tool, doesn't switch.
- **P0-A2**: Does NOT use `@latest` as the workaround. Pin to a specific version per slice 089 D2 rationale (floating pins create the class of failure where a green PR silently becomes a red PR).
- **P0-A3**: Does NOT promote the `Go · govulncheck` job to required-checks. That's a separate future slice with its own cost-of-blocking decision; this slice just fixes the broken install.
- **P0-A4**: Does NOT add the broken `@v1.1.3` pin to a `.tool-versions` denylist with an in-tree enforcement mechanism. Documenting the lesson in the decisions log is sufficient; a denylist file is overhead until we have a real catalog of bad pins.

## Skill mix (3–5)

- Go-toolchain dependency-graph reasoning (`x/tools` constant-folding compat by Go version)
- GitHub Actions YAML editing
- `engineering-advanced-skills:runbook-generator` (the decisions log records the pin-selection rationale)
- `simplify` (the fix is one line)

## Notes for the implementing agent

- **The actual failure log to read** is on PR #177 run `25946892152` job `76276715737` (and its post-merge twin). The install step's exit code is non-zero; the error message identifies `golang.org/x/tools@v0.23.0/internal/tokeninternal/tokeninternal.go:64`.
- **The fix is one workflow line.** Resist the temptation to refactor surrounding workflow blocks "while you're in there" — out-of-scope edits belong in their own slice per the spillover-as-slice convention.
- **Verify the new pin actually completes a scan**, not just installs. The install-step success is necessary but not sufficient; AC-3 requires the `govulncheck ./...` step to also produce output.
- **If the bumped pin still fails to install**, that's an escalation-worthy finding — record the exact error, file a follow-on slice 091 if needed, do not loop on pin-bumping in this slice.
- **If the bumped pin reports actual CVEs**, apply slice 089's AC-8 procedure verbatim: per-finding pick (bump-dep / suppress-with-justification / escalate-to-maintainer). The govulncheck CVEs are likely in transitive Go modules — `go mod tidy` after the bump may resolve some.
