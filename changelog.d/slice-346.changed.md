- **refactor(ci):** slice 346 — `.github/workflows/ci.yml` `tests-integration`
  job shrunk by 202 lines (463 → 261) by extracting 19 inline `# Slice NNN:
extended to include ...` enrolment-history commentary blocks to a sidecar
  doc at `docs/ci/integration-job-history.md`. The yaml retains live-behavior
  comments (`-p 1` race rationale + local reproducer, the Slice 036 MinIO /
  Slice 015 NATS `services:`-vs-`docker run` rationale, the Slice 033 RLS
  audit invariant-6 rationale, the Slice 279 merged-gate architecture, the
  Slice 297 TRUNCATE-before-down-migrations rationale) and a header pointer
  to the sidecar. Spillover from slice 334 framework audit finding I-2.
  Verification: `yq` round-trip of `.jobs.tests-integration` produces
  byte-identical JSON before and after the edit (no flag change, no
  package-list change, no service-container change). Total ci.yml line
  count 2488 → 2286. Decisions log:
  `docs/audit-log/346-ci-yaml-history-extraction-decisions.md`.
