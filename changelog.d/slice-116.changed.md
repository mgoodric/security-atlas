- **infra:** slice 116 — `Frontend · Playwright e2e` promoted to a
  required status check on `main`. `.github/branch-protection.json`
  adds the context; the slice-061 docs-only fastpath stub-twin
  (`frontend-playwright-stub`) is retained under the same check name
  so docs-only PRs continue to resolve the check in seconds.
  Maintainer-verified soak: ≥5 clean runs across slices
  142/143/198/201/202 (rationale on PR #492). `web/e2e/README.md`
  documents the new required-check status + seed-harness contract for
  spec authors. After merge: maintainer runs
  `bash scripts/apply-branch-protection.sh` to push the file's
  contexts list to live GitHub branch-protection. Closes the
  slice-069 → 079 → 082 quarantine arc.
