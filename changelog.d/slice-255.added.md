- **frontend:** slice 255 — control-detail header action buttons +
  "last evaluated" timestamp. Adds the three top-right action buttons
  the mockup (`Plans/mockups/control.html` lines 92-102) places in the
  control header — Run query · Edit YAML · Request exception — plus
  the "last evaluated `<relative-time>`" sub-line below them. JUDGMENT
  decisions (full trail at
  [`docs/audit-log/255-header-actions-decisions.md`](docs/audit-log/255-header-actions-decisions.md)):
  Run query + Edit YAML render as shadcn `<Button variant="outline"
size="sm" disabled>` with `title` + `aria-label` tooltips naming
  canvas §4.5 (control-as-code) and the v2 status — the slice 183 /
  slice 184 placeholder pattern, NOT `<a href="#">`. Request exception
  links to `/exceptions?control_id=<id>` — a merged URL-driven filter
  on the existing exceptions list page (slice 177); the destination is
  real, not a placeholder. The "last evaluated" timestamp reads
  `state.last_observed_at` from `GET /v1/controls/{id}/state` (the
  same source the right-rail freshness clock uses) and aggregates to
  the freshest cell via the same most-recent-wins reducer; a
  `useNow(60_000)` hook refreshes the relative-time string every
  60 seconds without forcing a data refetch. New pure helper
  `web/lib/relative-time.ts` exposes `relativeTime` / `relativeTimeOrNever`
  with an injectable clock for deterministic tests — 10 vitest cases
  cover the 8-minutes-ago mockup parity, singular/plural boundaries,
  sub-minute clamp to "just now", clock-skew future, hour/day
  escalation, null / undefined / unparsable input, and the
  `null → "never"` vs `undefined → "—"` semantic split. Playwright
  spec `slice 255: header action buttons + last-evaluated timestamp`
  in `web/e2e/control-detail.spec.ts` asserts AC-2 button render +
  order, AC-3/AC-4 disabled-with-tooltip semantics on the two v2
  buttons, AC-1 last-evaluated sub-line visible, P0-255-3 no
  `<a href="#">` in the action well, and AC-6 keyboard tab order
  (Run query → Edit YAML → Request exception). Anti-criteria honored:
  no Run query execution path (P0-255-1), no YAML editor (P0-255-2),
  no dead-link anchors (P0-255-3), no new API endpoint (P0-255-4).
