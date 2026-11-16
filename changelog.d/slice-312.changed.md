- **test(coverage):** slice 312 — round-3 coverage audit + thresholds
  refresh. Re-measured every Go package in the monorepo against the
  merged unit + integration profile (gocovmerge); the audit doc lives
  at `docs/coverage-audit-2026-05-round-3.md` with the per-package
  classification and disposition. Headline: **honest closure** — all 73
  currently-floored business packages are at-target (merged ≥ 70%),
  zero packages have eroded below their floor, and the remaining gap
  work is concentrated in 43 untracked packages added since slice 279
  (auth-substrate-v2, MCP, demo seed). Zero inline lifts; the audit
  decisions log at `docs/audit-log/312-coverage-audit-round-3-decisions.md`
  captures D1-D8 (lift-zero verdict, excludes additions, ratchet
  refreshes, spillover grouping rationale, measurement methodology).
  The thresholds refresh adds 13 new floors for already-good untracked
  packages (auth-substrate-v2 + MCP + export + platform + sdk-go/oauth
  - securityheaders + testjwt), ratchets 9 floors where measured has
    comfortably outgrown the existing floor (e.g.
    `internal/api/schemaregistry` 71 → 79 measured at 81.6%;
    `internal/risk` 71 → 77 measured at 79.5%; full list in the audit
    doc's "Ratchet-up opportunities" section), and adds 4 packages to
    `excludes` (CLI scripts + the 1-stmt embed in `catalogs/metrics` +
    the 0-stmt `internal/api/emptyset`). Long tail flows to 9 spillover
    slices (313 admin handlers · 314 OAuth AS · 315 auth-substrate-v2
    smalls · 316 HTTP handler enrollment trio · 317 MCP write-proposals
    · 318 audit ledger · 319 questionnaire engine · 320 demo seed · 321
    pkg/sdk-go) — within the P0-312-5 cap of 10. Coverage-gate passes
    cleanly against the slice-308 PR's CI merged-coverage artifact
    (run `26494738884`, 90 packages checked, 0 failed). Constitutional
    invariants P0-312-1 (monotonic ratchet) through P0-312-6 (audit
    distinguishes unit vs merged) all honored.
