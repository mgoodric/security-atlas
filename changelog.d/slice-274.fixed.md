- **e2e:** slice 274 — `web/e2e/settings.spec.ts` AC-9 (API tokens
  section renders empty-state or row table) no longer flakes /
  deterministically fails on `settings-token-row` count. Root cause:
  slice 249's SSR-prefetch of `is_admin` via the layout-level
  `HydrationBoundary` made `settings-section-tokens` (admin variant)
  visible on the first byte of SSR HTML, eliminating the implicit
  pre-slice-249 delay between the section becoming visible and the
  inner `useQuery(["settings-creds"])` list completing. AC-9's
  `.count()` snapshot fired while the list was still in `isLoading`
  (rendering `<Skeleton data-testid="settings-tokens-loading" />`)
  and returned 0. Fix: swap the `.count() > 0` snapshot for the
  auto-waiting `await expect(rows.first()).toBeVisible()` assertion
  (same pattern AC-11 already uses on line 328 of the same file).
  Empirically disproved the 4 spec-listed hypotheses (fixture
  cross-contamination via `audits-header.sql` / `settings.sql`
  shared user UUID; slice 250 `isCredentialBearer` interaction;
  Playwright worker parallelism between AC-9 and AC-11; schema
  drift on `api_keys`). Full diagnosis +
  reproduction methodology in
  `docs/audit-log/274-settings-ac9-token-row-flake-decisions.md`.
  `web/e2e/README.md` extended with a `### Timing-sensitive
assertions` subsection so future spec authors avoid the same
  snapshot-vs-async-fetch race. Production code untouched (the SSR
  prefetch is the intentional slice 249 fix; the test-side
  assertion shape was the bug).
