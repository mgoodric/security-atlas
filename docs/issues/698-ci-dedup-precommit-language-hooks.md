# 698 — De-duplicate the precommit CI job's language hooks

**Cluster:** CI / Infra
**Estimate:** S
**Type:** JUDGMENT
**Status:** `ready`
**Priority:** P3
**Spillover from:** slice 693 (pipeline-efficiency audit, Tier 2).

## Narrative

The CI `precommit` job runs `pre-commit run --all-files`, which re-executes language hooks
that dedicated jobs already cover:

- `gofmt` hook vs. `lint-go` (golangci-lint includes gofmt/goimports) — checked twice.
- `ruff` + `ruff-format` hooks vs. `lint-python` (`Python · ruff`) — direct duplication.
- `prettier` hook vs. `frontend-lint` — overlapping formatting concern.

The `precommit` job's UNIQUE value is the non-language hooks (trailing-whitespace,
end-of-file-fixer, check-yaml/json/toml, detect-private-key, detect-aws-credentials,
mixed-line-ending, actionlint). Run the CI precommit with
`SKIP=gofmt,ruff,ruff-format,prettier` so it owns only what no other job covers; the skipped
hooks stay enforced by lint-go / lint-python / frontend-lint.

JUDGMENT call (must verify before acting): confirm `.golangci.yml` actually enables the
`gofmt`/`goimports` linters before dropping the gofmt hook from CI — if it does not, KEEP
gofmt in the precommit job. The secret-detection hooks (`detect-private-key`,
`detect-aws-credentials`) are NEVER skipped (GitGuardian is a backstop, not a replacement).

## Acceptance criteria

- [ ] **AC-1.** Verify golangci-lint enables gofmt/goimports; record the finding in the PR.
- [ ] **AC-2.** The CI precommit job runs with `SKIP=gofmt,ruff,ruff-format,prettier` (minus
      any hook AC-1 shows is NOT covered elsewhere).
- [ ] **AC-3.** `detect-private-key` + `detect-aws-credentials` + check-yaml/json/toml +
      actionlint still run in the precommit job.
- [ ] **AC-4.** The LOCAL pre-commit (developer machines, `.pre-commit-config.yaml`) is
      UNCHANGED — only the CI invocation skips the duplicated hooks.
- [ ] **AC-5.** ruff/gofmt/prettier violations are still caught somewhere in CI (prove via the
      dedicated jobs).

## Anti-criteria

- Does NOT remove any hook from `.pre-commit-config.yaml` (local enforcement stays full).
- Does NOT skip any secret-detection or structural-validation hook in CI.
- Does NOT drop gofmt if golangci-lint is not actually enforcing it.

## Dependencies

- Pairs with slice 693 AC-5 (which CACHED the precommit job). Independent of it functionally.

## Notes

Source: slice 693 audit Finding 6A.
</content>
