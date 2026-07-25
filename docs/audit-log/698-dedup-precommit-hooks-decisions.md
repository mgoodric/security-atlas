# Slice 698 — De-duplicate the precommit CI job's language hooks — decisions log

JUDGMENT slice. The subjective build-time call here is the per-hook
skip-vs-keep decision for the CI `pre-commit · all hooks` job: each
language-format hook is skipped ONLY where a dedicated CI job provably
enforces the same concern, and KEPT (the conservative choice) where coverage
is not proven. Those calls and their proofs are recorded below per the
continuous-batch JUDGMENT convention; the maintainer iterates post-deployment.
This does NOT touch the product-runtime AI-assist boundary (separate,
constitutional). Source: slice 693 audit Finding 6A.

- detection_tier_actual: none (no bug surfaced; CI-config dedup)
- detection_tier_target: none

(No platform bug was introduced or fixed. The change is a declarative CI
invocation tweak validated by the pipeline itself on this PR — actionlint +
check-yaml + cache-path-guard + action-pin-check run over the ci.yml edit, and
the precommit job exercises its own new `SKIP=` env. The correct verification
tier for a CI-config change is "the PR's own CI run is green", so
`actual == target == none`.)

---

## D1 — Per-hook coverage verification (the whole slice)

Each candidate hook in {gofmt, ruff, ruff-format, prettier} was verified against
the dedicated jobs BEFORE being added to `SKIP`. A hook is skipped ONLY with
proven coverage; otherwise it is kept.

| Hook          | Disposition | Proof (dedicated job + step)                                                                                                                                                                                                                                                                                                                                                                                                                                                                 |
| ------------- | ----------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `gofmt`       | **SKIP**    | `.golangci.yml` `formatters:` block enables `gofmt` (line 40) + `goimports` (line 41); `Go · lint` runs `golangci-lint-action@v9` pinned to `v2.12.2` (ci.yml ~697–708). golangci-lint v2 reports enabled-formatter diffs as issues during the standard run, so the gofmt/goimports concern fails `Go · lint`.                                                                                                                                                                               |
| `ruff`        | **SKIP**    | `Python · ruff` runs `uv tool run ruff@0.7.0 check .` (ci.yml line 916) — whole-repo lint.                                                                                                                                                                                                                                                                                                                                                                                                   |
| `ruff-format` | **SKIP**    | `Python · ruff` runs `uv tool run ruff@0.7.0 format --check .` (ci.yml line 917) — whole-repo format check. BOTH the lint AND the format pass run, so `ruff-format` is genuinely covered (this was the conditional case in the brief; verified present).                                                                                                                                                                                                                                     |
| `prettier`    | **KEEP**    | NOT covered. `Frontend · lint` (ci.yml ~1306) is `working-directory: web` and runs only `npm run lint` = bare `eslint` (web/package.json line 14); no prettier, no format check. Root `package.json` has no `lint`/`format`/`prettier` script. The precommit prettier hook covers `[javascript, jsx, ts, tsx, json, yaml, markdown]` across the WHOLE repo (`.pre-commit-config.yaml` lines 45–47), including root-level markdown/YAML/JSON. Skipping it would lose that format enforcement. |

**Final SKIP list: `gofmt,ruff,ruff-format`** (3 of the 4 candidates).

## D2 — The prettier markdown/YAML coverage call (specifically)

The brief flagged prettier as the risky one. The slice-061 comment at ci.yml
~934–936 documents the precommit prettier's purpose explicitly: it auto-formats
markdown + YAML on docs-only PRs "so formatting nits never slip through." The
only other prettier-shaped job, `Frontend · lint`, is `web/`-scoped and runs
eslint (not prettier). There is therefore NO dedicated job covering the SAME
file set (root markdown / YAML / JSON, plus web TS/TSX _formatting_ as distinct
from eslint _linting_). Per AC-5 and the anti-criteria — "do not lose coverage"
— prettier is KEPT. This is the conservative, correct choice; skipping fewer
than all four candidate hooks is expected when coverage is not proven, and the
slice's value is the verified dedup, not maximal skipping.

## D3 — What is NEVER skipped (AC-3, hard constraint)

`SKIP` lists ONLY the three proven-covered language formatters. Every
secret-detection and structural hook stays active in the CI precommit job:
`detect-private-key`, `detect-aws-credentials`, `check-yaml`, `check-json`,
`check-toml`, `check-added-large-files`, `mixed-line-ending`,
`trailing-whitespace`, `end-of-file-fixer`, `actionlint`, `cache-path-guard`,
and `prettier`. GitGuardian is a backstop for secret detection, not a
replacement, so the secret hooks are never delegated away.

## D4 — Local enforcement unchanged (AC-4)

`.pre-commit-config.yaml` is UNTOUCHED. Developer machines running
`pre-commit run` (commit + push stages) keep the full hook set, including
gofmt / ruff / ruff-format. The `SKIP=` env lives ONLY on the CI job's
`Run all hooks against all files` step — it is a CI-invocation-time skip, not a
config-level removal. The `--all-files --show-diff-on-failure` flags and the
slice-693 cache step are unchanged; no job `name:` / `if:` / `needs:` /
required-check name changed (branch-protection invariant preserved).

## Revisit once in use

- If a future slice adds a dedicated prettier/format CI job covering the
  whole-repo markdown/YAML/JSON file set (e.g., a root `npm run format:check`),
  re-evaluate adding `prettier` to the `SKIP` list at that time.
- If `Python · ruff` ever drops its `ruff format --check .` step, `ruff-format`
  must be removed from `SKIP` immediately (coverage would no longer hold).
- If golangci-lint's formatters config or the `Go · lint` enforcement changes,
  re-verify the gofmt/goimports coverage before relying on the skip.
