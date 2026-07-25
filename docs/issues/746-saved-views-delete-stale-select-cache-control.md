# 746 — Saved-view delete leaves a stale `<select>` (BFF GET lacks `Cache-Control: no-store`)

**Cluster:** Frontend / BFF
**Estimate:** S
**Type:** AFK
**Status:** `ready`

## Parent / surfaced-by

Surfaced during slice 743 (controls-list e2e un-quarantine), captured as a
follow-up per the continuous-batch policy. The slice-448 saved-view delete
round-trip cannot be fully asserted by the e2e oracle because the deleted view
lingers in the `<select>` until a full page reload; slice 743 quarantined ONLY
that sub-assertion (with a cited reason in `web/e2e/controls-list.spec.ts`)
rather than weaken the spec — it stays off until this slice fixes the impl.

## The bug

After `DELETE /v1/saved-views/{id}`, the client refetches the saved-views list.
A fresh `GET /v1/saved-views` upstream returns the correct (now shorter) list,
but the **browser serves a cached response**: the BFF route handler that
forwards the saved-views GET (`forwardJSON`-style) does NOT set
`Cache-Control: no-store`, so the browser/React-Query refetch reads a stale
cached body and the deleted view still appears in the `<select>`. It only
disappears on a hard reload (cache-busting navigation).

## Fix shape

Add `Cache-Control: no-store` (and `Pragma: no-cache`) to the BFF response for
the saved-views GET (the `web/app/api/.../saved-views` route handler, or the
shared `forwardJSON` helper if scoped per-route). Saved-views are per-(tenant,
user) mutable state — they must never be browser-cached. Verify no OTHER GET
that legitimately wants caching is affected if you touch a shared helper (prefer
the per-route header to avoid a broad blast radius).

## Acceptance criteria

- [ ] **AC-1.** The saved-views BFF GET response carries `Cache-Control: no-store`.
- [ ] **AC-2.** After a saved-view delete, the refetched `<select>` reflects the
      deletion WITHOUT a page reload.
- [ ] **AC-3.** Re-enable the quarantined slice-448 saved-view-delete sub-assertion
      in `web/e2e/controls-list.spec.ts`; it passes the CI Playwright e2e job.
- [ ] **AC-4.** If a shared `forwardJSON`/BFF helper is changed, confirm no other
      route's caching behavior regresses (a targeted vitest on the route handler).
- [ ] **AC-5.** `npm run test` + `npm run lint` + `npm run typecheck` clean.

## Dependencies

- **#468** (`merged`) — ships the server-backed saved-views store.
- **#743** (`merged`) — un-quarantined the spec + filed this finding.

## Notes

The inline cited-reason quarantine lives near the saved-view-delete test in
`web/e2e/controls-list.spec.ts` (search "Cache-Control: no-store"). The root
cause is BFF-layer, not the upstream Go handler (which returns correct data).
