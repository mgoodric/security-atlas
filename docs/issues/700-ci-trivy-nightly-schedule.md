# 700 — Move the Trivy image scan off the PR hot-path to a nightly main run

**Cluster:** CI / Security
**Estimate:** S
**Type:** JUDGMENT
**Status:** `ready`
**Priority:** P3
**Spillover from:** slice 693 (pipeline-efficiency audit, Tier 2 — conservative).

## Narrative

The four dep/CVE scanners (govulncheck, npm-audit, Trivy, CodeQL) are mostly complementary,
not redundant — the audit found NO genuine scanner overlap to remove. The ONLY safe efficiency
move is SCHEDULING: `trivy-image` is the costliest of the advisory scanners (it does a full
local `docker build` of the atlas image on every code PR) and it is already non-blocking (NOT
in `.github/branch-protection.json` — newly-published CVEs must not flake unrelated PRs). Its
findings are driven by the CVE database, which changes independently of the PR diff.

Move **Trivy only** to a nightly `schedule:` run on `main` (keep govulncheck + CodeQL on the
PR — they are fast and diff-correlated). A PR that introduces a newly-vulnerable dependency is
still caught: the dep change is reviewable in the diff, govulncheck covers reachable Go CVEs at
PR time, and the nightly Trivy run narrows the image-CVE feedback gap to <24h.

This is filed P3 and deliberately conservative: if the maintainer values per-PR Trivy feedback,
this slice is a no-op WONTFIX. The decision is whether the <24h image-CVE feedback delay is an
acceptable trade for removing a full Docker build from every code PR. CodeQL and govulncheck
MUST stay on the PR — they are not in scope to move.

## Acceptance criteria

- [ ] **AC-1.** A nightly (or daily) `schedule:` job on `main` runs the Trivy image scan with
      the SAME config (severity, `vuln-type: os,library`, ignore-file) as the current PR job.
- [ ] **AC-2.** The PR-time `trivy-image` job is removed (or gated off PRs) — and its `-stub`
      twin handled so no required check dangles.
- [ ] **AC-3.** govulncheck, npm-audit (maintainer's call), and CodeQL remain on the PR path.
- [ ] **AC-4.** A Trivy finding on the nightly run is visible/actionable (job summary or an
      auto-filed issue), not silently buried.
- [ ] **AC-5.** No REQUIRED check is removed (Trivy is already advisory-only — verify against
      `.github/branch-protection.json`).

## Anti-criteria

- Does NOT move CodeQL or govulncheck off the PR path.
- Does NOT remove Trivy coverage — only reschedules it.
- Does NOT reduce Trivy's severity sensitivity or scan scope.
- If the maintainer prefers per-PR Trivy, this slice is WONTFIX — do not force it.

## Dependencies

- Independent. Composes with slice 694 (if Trivy stays on PR, cache its build instead).
  694 and 700 are alternatives: cache it, or move it. Pick one.

## Notes

Source: slice 693 audit Finding 2A. Conservative-by-design — the safe win is rescheduling the
single most database-driven (least diff-correlated) advisory scanner.
</content>
