- **e2e:** slice 275 — `web/e2e/control-detail-tabs.spec.ts` AC-1 / AC-2
  / AC-8 (both variants) / AC-9 no longer time out on
  `getByTestId("control-tabs")` deterministically. Root cause: the
  page-mount sequence on `/controls/{id}` (Suspense fallback →
  `ControlDetailPageInner` mount → `useSearchParams` resolution →
  `coverageQ` `useQuery` fire → BFF + mock-fulfilled fetch → React
  commit) routinely exceeds Playwright's default 5s `toBeVisible`
  timeout under CI load (2 parallel workers, shared docker stack).
  The page stays in its `coverageQ.isLoading` Skeleton branch
  (testid `control-detail-loading`); the assertion against
  `control-tabs` snapshots before the tablist renders. Fix:
  introduce a `gotoControlDetail(page, opts)` helper that gates the
  next assertion on `page.waitForResponse("**/coverage")` (set up
  BEFORE `page.goto` per Playwright invariant), and raise the first
  visibility assertion's timeout to 30s as a CI-load backstop. All
  seven tests in the file route through the helper; AC-8 refresh
  additionally awaits a second coverage response on `page.reload()`
  since the in-flight TanStack Query restarts on remount. Empirically
  disproved the 3 spec-listed hypotheses (Suspense + 5s timeout —
  the partial truth; `page.route` registers after `page.goto` —
  false, every `await` resolves before the test body; `enabled:
Boolean(id)` Suspense race — false, the query just takes longer
  than 5s). Full diagnosis + the slice 274 → 275 pattern
  generalisation in
  `docs/audit-log/275-slice-254-tabs-e2e-fix-decisions.md`.
  `web/e2e/README.md` extended with a `### Gating the FIRST
visibility assertion on a network round-trip` subsection under
  the existing slice 274 timing-sensitive-assertions block.
  Production code untouched (P0-275-2); the slice 254 source is
  correct — only the e2e assertion shape was the bug. Closes the
  spillover thread filed from slice 254's force-merge.
