# CI artifact retention

Artifact and cache storage is the only GitHub Actions cost this repo
incurs: minutes are free on a public repo, and the September 2026 bill
($16.22) was all "Actions storage". An `upload-artifact` step without
`retention-days` keeps the repo-level default (90 days unless changed in
the repo settings), so every upload step
declares its own.

## Defaults

| Artifact                                                                                                   | `retention-days`          |
| ---------------------------------------------------------------------------------------------------------- | ------------------------- |
| Playwright reports and traces (`playwright-report`, `playwright-report-prod-build`, `ui-honesty-report-*`) | 3                         |
| Everything else (coverage, reports, the prebuilt `atlas-binary`)                                           | 7                         |
| Read only by a later job in the same run (`container-publish.yml` digests)                                 | 1 (may be shorter than 7) |

A value **longer** than 7 is allowed only when something reads the
artifact after its run finishes (another workflow, a scheduled job, a
release). That step carries an inline comment naming the reader and why
it needs the time.

## Who reads artifacts today

Every `download-artifact` in `.github/workflows/` reads an artifact
uploaded earlier in the **same run** (`atlas-binary` for the e2e jobs,
the coverage profiles for the merged-coverage job, the per-platform
digests for the manifest merge). No workflow, script or the flake
counter (`scripts/flake-counter.sh`, which reads run conclusions via the
API, not artifacts) downloads an artifact from another run, so no step
needs more than 7 days.

## Adding an upload step

Set `retention-days` in the same `with:` block, using the table above.
If the artifact must outlive its run, add the comment naming its reader.
