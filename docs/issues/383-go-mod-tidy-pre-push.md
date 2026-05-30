# 383 — Pre-push `go mod tidy` drift check (catch direct-import-not-promoted locally)

**Cluster:** Infra
**Estimate:** 0.5d
**Type:** AFK
**Status:** `ready`

## Narrative

Slice 377 surfaced a class of bug where engineers add a new direct import in Go but don't promote it from the `// indirect` block in `go.mod` to the direct `require` block. The local feedback loop (`go test ./...`, `go test -race ./...`, even `golangci-lint`) all pass cleanly because the dependency is already in `go.sum` as an indirect transitively. CI's `Go · build + test` job runs `go mod tidy && git diff --exit-code go.mod go.sum` as a drift check, which promotes the import and produces a non-empty diff — that's the gate that fails.

Cost of the bug, per occurrence: ~10 min orchestrator wall-clock (investigate CI log, run `go mod tidy` locally, commit the 1-line edit, force-push, CI re-cycle). Slice 377 (PR #848) hit this; orchestrator-pushed fix at `f11469ee` after the engineer's commit `adb6fba0` failed CI. Recurring potential: any future direct import that was previously indirect hits the same class of bug.

**The fix.** Add a pre-push hook (via the existing `.pre-commit-config.yaml`) that runs `go mod tidy && git diff --exit-code go.mod go.sum`. If the diff is non-empty, fail with an actionable message naming the file(s) drifted + the corrective command. The hook is defense-in-depth: CI is still the authoritative gate, but the local pre-push catches the same class earlier.

**Scope discipline.**

- DOES NOT change the CI gate — CI still runs the same `go mod tidy` drift check.
- DOES NOT auto-run `go mod tidy` and stage the result. Engineers must explicitly run `go mod tidy` and commit the changes. The hook is detect-only, not auto-fix. (Auto-fix would silently rewrite go.mod without the engineer's awareness — bad pattern for a security-aware project.)
- DOES NOT apply to non-Go dep manifests (`web/package.json` + `web/package-lock.json` are gated by the existing `npm-lint-web` pre-commit hook).
- DOES NOT slow pre-push noticeably. `go mod tidy` on this codebase runs in <1 second when no changes are required (no module downloads triggered).

**Why now.** Session-stability analysis on 2026-05-29 surfaced this as the second of two highest-leverage process fixes (alongside slice 382 STATUS-row enforcement). User-confirmed file.

**Trigger.** Slice 377 (PR #848) batch 159; documented in `project_batch_159_closed.md` as the canonical "go-mod-tidy lesson." Class also matches the existing `feedback_local_vs_ci_delta.md` user memory.

## Threat model

CI-config edit + pre-push hook. STRIDE pass:

- **S (Spoofing):** CLEAN. No new auth surface added or modified.
- **T (Tampering):** Pre-push hook is local-only — an attacker who can run arbitrary commands in the engineer's shell could disable it. But that attacker would already be inside the trust boundary. CI's authoritative drift check still runs server-side. CLEAN — defense-in-depth, not the only gate.
- **R (Repudiation):** CLEAN. Hook produces local stderr only; no audit-log writes.
- **I (Information disclosure):** CLEAN. Hook just compares file contents; no info disclosure.
- **D (Denial of service):** Pre-push wall-clock added <1 second per push. No reasonable threat surface.
- **E (Elevation of privilege):** CLEAN. No role check added or modified.

**Threat-model verdict:** CLEAN.

## Acceptance criteria

- [ ] **AC-1.** New local hook entry in `.pre-commit-config.yaml` under the `pre-push` stage that runs `go mod tidy && git diff --exit-code -- go.mod go.sum`. The hook's `id` is `go-mod-tidy-drift`. The hook's `language` is `system` (delegates to local `go` binary).
- [ ] **AC-2.** Hook is gated by `files: ^(go\.mod|go\.sum|.*\.go)$` so it only runs on pushes that touch Go-related files. Engineers pushing pure docs/frontend changes don't pay the latency.
- [ ] **AC-3.** Hook failure message is actionable: "go.mod / go.sum drift detected. Run `go mod tidy` and commit the result before pushing. Diff:" followed by the `git diff --stat go.mod go.sum` output.
- [ ] **AC-4.** Hook exits 0 (passes) when no drift is detected — the no-op fast path.
- [ ] **AC-5.** New `just verify-go-mod-tidy` recipe in `justfile` that invokes the same check, so engineers can run it manually without triggering a push attempt.
- [ ] **AC-6.** Synthetic-positive test: introduce a 1-line stub Go file importing a package known to be `// indirect` in `go.mod` (e.g. `import _ "go.opentelemetry.io/otel/metric"`), attempt `git push`, observe the hook failure. Document the verification in the decisions log; revert the synthetic stub before merging.
- [ ] **AC-7.** Synthetic-negative test: a Go-only commit that doesn't touch dep manifests pushes cleanly. Document the verification.
- [ ] **AC-8.** `CLAUDE.md` "Working norms" gains a one-line entry: "Engineers run `just verify-go-mod-tidy` (or push, which triggers the same check via pre-push hook) before declaring Go work complete."
- [ ] **AC-9.** Decisions log at `docs/audit-log/383-go-mod-tidy-pre-push-decisions.md` records: D1 hook location (`.pre-commit-config.yaml` pre-push stage vs `.git/hooks/pre-push` script — recommend pre-commit-config for consistency with existing hooks); D2 the `files:` regex final form; D3 whether to fail-on-warn-only or hard-fail (recommend hard-fail per slice 382 reasoning).
- [ ] **AC-10.** `pre-commit run --all-files` passes after the new hook is added; new hook doesn't break any existing flow.

## Constitutional invariants honored

- **No change to data flow or persistence layer.** Pure tooling/process discipline.
- **Defense-in-depth.** Pre-push hook is one layer; CI's `go mod tidy` drift check remains the authoritative gate. Pre-push catches earlier; CI catches anything that slips through.

## Canvas references

- None directly. This slice is tooling discipline; the canvas describes product architecture.

## Dependencies

None. Pure config slice.

## Anti-criteria (P0 — block merge)

- **P0-383-1.** Does NOT auto-run `go mod tidy` and stage the changes. Detect-only. Auto-fix would silently rewrite `go.mod` without engineer awareness, and a security-aware project should never silently rewrite dependency declarations.
- **P0-383-2.** Does NOT apply to non-Go projects (`web/package.json` flows already have their own gates via npm + the existing `npm-lint-web` hook).
- **P0-383-3.** Does NOT widen scope to ratcheting golangci-lint OR adding new lint rules. This slice is `go mod tidy` only.
- **P0-383-4.** Does NOT slow pre-push by more than 1 second on a no-op push (the `files:` regex gate must short-circuit on non-Go pushes).
- **P0-383-5.** Does NOT require engineers to install a new tool — `go` binary is already required for the project.
- **P0-383-6.** Does NOT touch CI configuration. CI's drift check is unchanged.
- **P0-383-7.** Does NOT introduce new dependency.

## Skill mix (3-5)

- `pre-commit` framework editing (hook definition + stage selection)
- Bash scripting (the hook command itself)
- Markdown editing for CLAUDE.md "Working norms" entry
- Synthetic-test discipline (positive + negative tests, revert before merge)

## Notes for the implementing agent

### Phase-2 grill output (from /idea-to-slice 2026-05-29)

- **Domain model:** "pre-push hook" + "drift check" are clean terms. No drift vs existing pre-commit / `golangci-lint` / `actionlint` terminology in the repo.
- **Scope:** single coherent vertical (one hook addition + one docs note + synthetic tests).
- **Already-built check:** no prior slice addresses pre-push `go mod tidy`. Closest analog is the `npm-lint-web` hook (slice unknown) which gates JS-side lint. This slice's `go-mod-tidy-drift` hook is the Go-side equivalent for dep manifest drift.
- **Hidden finding:** The `.pre-commit-config.yaml` already runs hooks at `commit` stage; this slice adds at `pre-push` stage. The framework supports it (`stages: [pre-push]`) but the project may not have any pre-push hooks today. Engineer verifies in OBSERVE phase + records in D1.

### Phase-3 threat-model output

CLEAN. Pre-push hook is a defense-in-depth measure; CI authoritative gate unchanged.

### Implementation hints

- **`.pre-commit-config.yaml` shape** (proposed; engineer locks in D1):

  ```yaml
  - repo: local
    hooks:
      - id: go-mod-tidy-drift
        name: go mod tidy drift check
        entry: bash -c 'go mod tidy && git diff --exit-code -- go.mod go.sum'
        language: system
        stages: [pre-push]
        files: ^(go\.mod|go\.sum|.*\.go)$
        pass_filenames: false
  ```

- **`justfile` recipe** (proposed):

  ```
  verify-go-mod-tidy:
      go mod tidy
      git diff --exit-code -- go.mod go.sum
  ```

- **Synthetic-positive test artifacts** (AC-6): use `import _ "go.opentelemetry.io/otel/metric"` in a throwaway `internal/scratch/main.go` since `otel/metric` is the historical indirect-to-direct promotion candidate (slice 377). Revert before merge.
- **Synthetic-negative test artifacts** (AC-7): touch any `.go` file that doesn't change imports.

### Cross-references

- `project_batch_159_closed.md` — go-mod-tidy lesson (canonical incident report)
- `project_batch_158_closed.md` — performance audit slice 332 (which surfaced F-OPA-1; slice 377 closed it but in the process exposed this gap)
- `feedback_local_vs_ci_delta.md` (user memory) — class of bug this slice closes
- `.pre-commit-config.yaml` — hook destination
- `justfile` — recipe destination
- `CLAUDE.md` "Working norms" — one-line addition

### Why this slice now

Session-stability analysis 2026-05-29 — second of two highest-leverage process fixes (sibling to slice 382 STATUS-row enforcement). 1 incident this session × ~10 min orchestrator wall-clock = real cost, recurring potential. CI guard makes the convention self-enforcing locally so engineers can fix the gap themselves without a CI failure round-trip.
