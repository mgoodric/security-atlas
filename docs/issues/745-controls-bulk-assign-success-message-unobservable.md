# 745 — Controls bulk-assign success message is structurally unobservable

**Cluster:** Frontend
**Estimate:** S
**Type:** JUDGMENT
**Status:** `ready`

## Parent / surfaced-by

Surfaced during slice 743 (controls-list e2e un-quarantine), captured as a
follow-up per the continuous-batch policy. The slice-468 bulk-assign-owner
success confirmation (`controls-bulk-assign-message`) cannot be asserted by the
e2e oracle because of a render-lifecycle bug; slice 743 quarantined ONLY that
sub-assertion (with a cited reason in `web/e2e/controls-list.spec.ts`) rather
than weaken the spec — it asserts the observable success effect (the selection
clears) and leaves the message assertion off until this slice fixes the impl.

## The bug

The bulk-assign success handler sets the success message AND clears the
selection (`setSelected(new Set())`) in the **same batched React update**. The
success message is rendered **inside the selection bar**, which is conditionally
mounted on `selected.size > 0`. So the same update that sets the message
unmounts the element that would display it — the message never reaches the DOM
(or flashes for less than a frame). The operator gets no confirmation that a
bulk owner-assignment succeeded; only the selection clearing implies it.

## Fix shape (JUDGMENT — implementer picks)

Surface the confirmation OUTSIDE the unmounting selection bar so it survives the
selection-clear. Options (record the choice in the decisions log):

- A persistent status/toast region above the table (rendered independent of
  `selected.size`), shown for N seconds after a successful bulk-assign.
- Or keep the selection bar mounted briefly after success (defer the
  `setSelected(new Set())` clear) so the inline message is observable, then
  clear. (Less clean — couples confirmation timing to selection state.)

Prefer the independent region. Keep the existing `controls-bulk-assign-message`
testid on the visible element so slice 743's quarantined assertion can be
turned back on.

## Acceptance criteria

- [ ] **AC-1.** After a successful `POST /v1/controls:bulk-assign-owner`, the
      success confirmation is rendered in a region that is NOT unmounted by the
      selection-clear, and remains observable (testable) after `selected` resets.
- [ ] **AC-2.** The element carries the `controls-bulk-assign-message` testid.
- [ ] **AC-3.** Re-enable the quarantined slice-448/468 message sub-assertion in
      `web/e2e/controls-list.spec.ts` (remove the cited-reason quarantine for the
      message; the assertion passes in CI's `Frontend · Playwright e2e`).
- [ ] **AC-4.** `npm run test` (vitest) + `npm run lint` + `npm run typecheck` clean.
- [ ] **AC-5.** No change to the bulk-assign API or authz; pure FE render fix.

## Dependencies

- **#468** (`merged`) — ships the bulk-assign surface.
- **#743** (`merged`) — un-quarantined the spec + filed this finding.

## Notes

The inline cited-reason quarantine lives near the slice-448-AC-1 test in
`web/e2e/controls-list.spec.ts` (search "unmounted with the selection bar").
Pure FE; no backend change.
