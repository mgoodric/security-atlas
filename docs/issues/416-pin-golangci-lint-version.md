# 416 — Pin golangci-lint version (drop `version: latest`)

**Cluster:** Infra
**Estimate:** 0.5d
**Type:** AFK
**Status:** `ready` (no unmerged deps)

## Narrative

**WHY.** `.github/workflows/ci.yml` (the `lint-go` job, ~line 597) invokes
`golangci/golangci-lint-action` with `version: latest`:

```yaml
- uses: golangci/golangci-lint-action@82606bf257cbaff209d206a39f5134f0cfbfd2ee # v9
  with:
    version: latest
```

`Go · lint` is a **required** check (it is in `branch-protection.json`'s
`required_status_checks.contexts`). `version: latest` means the linter binary floats: when
golangci-lint cuts a new release, CI silently picks it up on the next run. A new release
routinely (a) enables new analyzers or changes existing analyzer behavior — failing PRs
that touched no relevant code — and (b) changes the embedded linter-cache key, busting the
warm cache and slowing the job. This is the **same class of failure** as slice 090's
`govulncheck@latest` incident, where a floating tool version turned a green required check
red with no code change. Every other tool in the toolchain is version- or SHA-pinned
(`sqlc` to `v1.31.1` in the `justfile` per slice 109; the action itself is SHA-pinned to
`82606bf` per slice 128; Go to `1.26`) — `golangci-lint`'s _binary version_ is the lone
floating dependency on a required check.

**WHAT.** Pin the linter to an explicit version, mirroring the `SQLC_VERSION`-in-`justfile`
pattern: replace `version: latest` with a concrete `version: v<X.Y.Z>` in the `lint-go`
job. Confirm that version is consistent with what contributors run locally (the `justfile`
lint target and/or `.pre-commit-config.yaml`), so local and CI lint agree. Version bumps
then land in dedicated, auditable PRs — a maintainer reviews the new analyzer set rather
than discovering it as a surprise red check on an unrelated PR.

**SCOPE DISCIPLINE.** This slice pins ONE version field. It does NOT introduce or remove
any linter, does NOT re-tune `.golangci.yml` rule config (a pin may surface that the
currently-floating `latest` already enabled a rule the config didn't expect — if so, that
fix-up is in scope only to the extent needed to keep `Go · lint` green at the pinned
version; broader linter-config curation is a separate slice). It does NOT pin Python `ruff`
or frontend `eslint` (those are tracked by their own toolchains; this slice is golangci
only).

## Threat model

STRIDE pass. Pinning a linter version is a supply-chain control: the _current_ `latest`
state is the threat (an unaudited, floating dependency on a required gate), and the pin is
the mitigation. CI changes that can mask failures get a real threat model, not a stamp.

**S — Spoofing.** No new endpoint or identity. No change.

**T — Tampering (supply-chain integrity — the primary threat).** **T-1 (floating linter =
unaudited code-path change on a required gate).** `version: latest` means an external party
(the golangci-lint maintainers, or anyone who can publish a malicious release if upstream
were compromised) can change what the _required_ `Go · lint` check does, on the project's
next CI run, with zero review. Pinning to an explicit version closes this: a version change
becomes an explicit, reviewable diff in `ci.yml`. The action _binary_ is already SHA-pinned
(slice 128); this slice extends the same discipline to the _linter version the action
installs_. Mitigation = the pin itself (AC-1).

**R — Repudiation.** Version bumps become git-attributable (a diff line in `ci.yml`) rather
than an invisible runtime resolution. This _improves_ the audit trail. No regression.

**I — Information disclosure.** No tenant data, no secrets in scope. The lint job reads
source and reports findings; pinning does not change its outputs' confidentiality.

**D — Denial of service.** **D-1 (the pin is the fix for an existing DoS-of-velocity).** A
floating `latest` can red-wall every open PR the moment upstream ships a stricter default —
a self-inflicted availability hit on merge throughput (exactly slice 090's govulncheck
incident). The pin removes that failure mode. The pin does NOT introduce a new DoS: a stale
pin is a known, controlled state, not an outage.

**E — Elevation of privilege.** No role boundary in scope. No change.

**Verdict: has-mitigations (the slice IS the mitigation for T-1/D-1).** Net security
posture improves; the only residual is the standard "pinned tool can drift behind upstream
fixes" trade-off, addressed by the dedicated-bump-PR convention (AC-4) and noted as
acceptable per the project's existing pin-everything posture.

## Acceptance criteria

- [ ] **AC-1.** The `lint-go` job in `.github/workflows/ci.yml` pins
      `golangci/golangci-lint-action` to an explicit `version: v<X.Y.Z>` — `version: latest`
      no longer appears for golangci-lint anywhere in `ci.yml`.
- [ ] **AC-2.** The pinned version is one that passes `Go · lint` clean against current
      `main` (if the pin surfaces a pre-existing finding that `latest` was already
      enforcing, the minimal fix to keep the required check green is included in this PR).
- [ ] **AC-3.** The CI-pinned version matches the version contributors run locally — the
      `justfile` lint target and/or `.pre-commit-config.yaml` golangci hook reference the
      SAME version (or a single source-of-truth variable is introduced and both reference
      it). Local-vs-CI lint parity holds.
- [ ] **AC-4.** A short note (in `ci.yml` comment or `CONTRIBUTING.md`) documents that
      golangci-lint version bumps land in dedicated PRs, mirroring the `SQLC_VERSION` bump
      convention.
- [ ] **AC-5.** `pre-commit run --all-files` and `Go · lint` both pass on the slice's PR at
      the pinned version (proves CI and local agree).
- [ ] **AC-6.** The `golangci/golangci-lint-action` `uses:` line REMAINS SHA-pinned
      (slice 128 `actions-pin-check` invariant is not regressed by editing the `with:` block).

## Constitutional invariants honored

- Tech-stack lock (CLAUDE.md): `golangci-lint (strict)` is the committed Go linter; this
  slice pins its version, it does not swap the tool.
- Supply-chain SHA-pinning (slice 128 `actions-pin-check`): the action stays SHA-pinned;
  the linter _version_ gains an explicit pin — extending, not violating, the discipline.
- "No floating dependency on a required check" — the implicit policy slice 090 / 109 / 159
  already established for govulncheck / sqlc.

## Canvas references

- `Plans/canvas/09-tech-stack.md` (Go tooling row — `golangci-lint (strict)` enforced via
  pre-commit + CI).
- CLAUDE.md tech-stack table, "Go tooling" + "DB access (sqlc pinned to v1.31.1)" rows — the
  pin-everything precedent.

## Dependencies

- None unmerged. (Slice 128 `actions-pin-check` and slice 109 `SQLC_VERSION` pin are both
  `merged` and set the precedent; they are not blockers.)

## Anti-criteria (P0 — block merge)

- **P0-1.** Does NOT add, remove, or disable any individual linter/analyzer in
  `.golangci.yml` beyond the minimal fix (if any) needed to keep `Go · lint` green at the
  pinned version. Linter-config curation is a separate slice.
- **P0-2 (security).** Does NOT regress the SHA-pin on the
  `golangci/golangci-lint-action` `uses:` line — editing `with:` must not touch the `@<sha>`.
- **P0-3.** Does NOT introduce CI-vs-local lint divergence — if a single-source-of-truth
  version is not used, the CI value and the local value MUST be the same literal.
- **P0-4.** Does NOT pin Python `ruff` or frontend `eslint` — out of scope; golangci only.
- **P0-5.** Does NOT auto-merge; maintainer reviews the chosen version.

## Skill mix (3-5)

`ci-cd-pipeline-builder` · `dependency-auditor` · `simplify` · `grill-with-docs`.

## Notes for the implementing agent

**Grill output (Phase 2):**

- _Terminology._ Distinguish the **action SHA** (already pinned, slice 128) from the
  **linter binary version** (the `with: version:` field — the floating one). The pin in
  this slice is the latter.
- _Already-built check._ `rg -l "golangci.*version|version: latest" docs/issues/` returns
  nothing dedicated — no prior slice pins this. The precedent slices (090 govulncheck, 109
  sqlc) pin _other_ tools; this is the golangci gap they imply but never closed.
- _Scope._ Resist scope-creep into "audit the whole `.golangci.yml`". The slice is one pin +
  whatever minimal fix keeps the gate green.

**Threat-model context (Phase 3).** T-1 is the whole point: `latest` is a floating,
unaudited dependency on a _required_ check. Slice 090 already demonstrated the concrete harm
with govulncheck. The pin converts an invisible upstream change into a reviewable diff.

**Implementation note.** Check current upstream: pick the latest _stable_ golangci-lint v2.x
release that is compatible with the repo's Go `1.26` toolchain and the existing
`.golangci.yml` schema (golangci-lint v2 changed the config schema vs v1 — if the repo's
config is v1-format, either pin the last v1-compatible release OR migrate the config; record
which in the decisions log if a JUDGMENT call is needed, though this slice is typed AFK on
the assumption a clean same-schema pin exists). Verify against `Go · lint` locally before
push.

**Provenance.** Filed 2026-06-03 in the CI-backlog batch (415-420). Same failure class as
slice 090's govulncheck-@latest incident; this closes the analogous golangci gap.
