# 082 — Pre-push hook: add `npm run lint -w web` once slice 078 lands

**Cluster:** Infra
**Estimate:** 0.25d
**Type:** AFK

## Narrative

Follow-on to slice 081. Slice 081 populated the existing `pre-push` hook slot via pre-commit-framework's `pre-push` stage (wired into `just install-hooks`). At slice-081's run-time (2026-05-15), slice 078 (`npm run lint` unblock after ESLint 10 + react-plugin incompat) was `ready` but **not merged**, so per slice 081 AC-7 + P0-A3, `npm run lint -w web` was deliberately omitted from the hook (adding it would have broken every engineer's push immediately, since `npm run lint` currently crashes on every React file).

Once slice 078 lands, the pre-push hook should ALSO run `npm run lint -w web` locally so ESLint errors are caught before push — matching the new informational `Frontend · lint` CI job that slice 078 will add.

## Acceptance criteria

- [ ] AC-1: Slice 078 is merged on `main` before this slice runs (dependency gate)
- [ ] AC-2: Pre-push hook configuration extended to invoke `npm run lint -w web` (or `just lint-frontend`) on the `pre-push` stage. Exact mechanism is a build-time judgment call:
  - Option A: add a `local` `repos:` entry to `.pre-commit-config.yaml` with `stages: [pre-push]`, `entry: npm run lint -w web`, `pass_filenames: false`
  - Option B: write a thin wrapper script invoked from the pre-commit-framework hook
  - Option C: split the `just install-hooks` recipe to install lint as a separate pre-push hook
    Engineer picks the smallest viable option at slice-run-time.
- [ ] AC-3: Test locally: introduce a deliberate ESLint-breakable change (`web/app/page.tsx` has an unused variable, for example), attempt `git push`, confirm the hook blocks. Record the result in the decisions log.
- [ ] AC-4: CONTRIBUTING.md "Local CI parity" subsection updated: drop the "until then it is limited to the pre-commit suite" caveat, replace with "and `npm run lint -w web` for frontend ESLint."
- [ ] AC-5: `docs/audit-log/082-pre-push-hook-add-lint-decisions.md` records the option chosen (A/B/C) + rationale + AC-3 test result.

## Constitutional invariants honored

- **Working norms — Surgical fixes**: smallest viable change. One config block + one CONTRIBUTING line edit.
- **AI-assist boundary**: nothing AI-generated.

## Canvas references

- _(none — local-dev infrastructure; canvas doesn't speak to git hooks)_

## Dependencies

- **078** (Unblock `npm run lint` after ESLint 10 + react-plugin incompat, ready) — **hard blocker**. This slice cannot run until 078 merges, because adding the lint invocation before 078 lands would break every push.
- **081** (Pre-push hook + slice-template step 9a guidance, in-progress at follow-on-creation time) — **soft dependency**. 081 puts the pre-push hook in place; 082 extends it.

## Anti-criteria (P0 — block merge)

- **P0-A1**: Does NOT relax the bypass — `git push --no-verify` MUST continue to work (inherited from 081 P0-A1).
- **P0-A2**: Does NOT add the lint invocation BEFORE slice 078 is merged. AC-1 is the gate.
- **P0-A3**: Does NOT add `npm run lint` (workspace-root) — the project's lint surface is workspace-scoped (`npm run lint -w web`); adding the root-level variant would run a broader scope than necessary.

## Skill mix (3–5)

- Pre-commit-framework configuration (the `local` repo + `pre-push` stage pattern)
- The `just install-hooks` recipe and its observed behavior
- `simplify` (the addition stays tight)
- AC-3's deliberate-failure test (parallel to slice 081 AC-5)

## Notes for the implementing agent

- Inherits the same hook delivery path slice 081 established. Don't reintroduce husky/lefthook.
- Don't bundle a slice-078-status-recheck step into this slice — the dependency is named explicitly; if 078 has somehow reverted, file a separate fix.
- AC-3's test is the verification this slice did its job. If the hook does not block, the slice is not done.
