# 745 — Surface the bulk-assign success message outside the unmounting selection bar — decisions log

JUDGMENT slice. Claude made the build-time render-shape call below (where the
confirmation lives, what it looks like, and how it dismisses) and recorded it
here; the slice ships when CI is green (no human sign-off gate). Pure FRONTEND
render fix — no backend / API / authz change (AC-5).

- detection_tier_actual: playwright
- detection_tier_target: playwright

The bug was found at the Playwright tier (slice 743's controls-list e2e
quarantined the message sub-assertion with a cited reason because the message
was structurally unobservable) and the fix is verified at the same tier (slice
743's assertion, re-enabled here, is the oracle — it now passes). This is the
correct detection tier for a render-lifecycle bug: it is invisible to the
node-env vitest tier (no JSX/DOM there per slice 069 P0-A3) and only a
browser-rendered selection→success→clear sequence exposes the
unmount-in-the-same-batch race. No new bug surfaced during the slice.

---

## The bug (confirmed before the fix)

`web/app/(authed)/controls/page.tsx` held the bulk-assign `assignMessage` in
React state and passed it as a prop into `<SelectionBar>`, which the page only
mounts when `selected.size > 0`. The bulk-assign `onSuccess` handler called
`setAssignMessage({kind:"ok", …})` AND `setSelected(new Set())` in the **same
batched React update**. So the same update that set the message dropped
`selected.size` to 0 and unmounted the selection bar — and with it the
`controls-bulk-assign-message` span — in the same render the message was set.
The POST genuinely succeeded (200, owner row written); only the inline
confirmation was never painted. The operator's only signal that a bulk
owner-assignment worked was the selection silently clearing.

## Decisions made

### D1 — Render the confirmation in a PERSISTENT region above the table, independent of `selected.size`. **(Confidence: high — THE central JUDGMENT call)**

**Alternatives weighed (the spec named both):**

| Option                                                                                                   | Shape                                                                                                                                            | Why / why not                                                                                                                                                                                                                                                                                                                                                               |
| -------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **(a) CHOSEN — persistent `BulkAssignMessage` region above the table**                                   | a new presentational component the page renders unconditionally (it returns `null` when `message` is null), outside the `selected.size > 0` gate | Decouples the confirmation's lifetime from the selection state entirely. The message survives the selection-clear because the element that displays it is never unmounted by it. This is the spec's preferred shape ("Prefer the independent region").                                                                                                                      |
| **(b) Defer the `setSelected(new Set())` clear** so the inline message stays mounted briefly, then clear | keep the message in the selection bar, delay the unmount                                                                                         | Rejected. The spec calls this "less clean — couples confirmation timing to selection state". It would make the confirmation's observability depend on a timing race against the selection-clear, re-introducing a subtler version of the same fragility (and the operator would see their selection linger after they triggered the action, which reads as "did it work?"). |

**Chosen: (a).** The confirmation is a page-level status, not a property of the
selection. Its home is the page body, above the table, next to the other
page-level status surfaces (the slice-224 scope-cap `Alert`). The selection bar
goes back to being purely about the live selection.

### D2 — Keep the `controls-bulk-assign-message` testid, drop the inline span from `SelectionBar`. **(Confidence: high — AC-2 + AC-3)**

The testid moved with the content onto the new `BulkAssignMessage` element;
the old inline `<span data-testid="controls-bulk-assign-message">` inside
`SelectionBar` was removed (along with the `assignMessage` prop, now unused).
One element carries the testid, so slice 743's re-enabled assertion
(`getByTestId("controls-bulk-assign-message")`) resolves to exactly the
now-observable element.

### D3 — Preserve `role="status"` + `aria-live="polite"`. **(Confidence: high)**

The original inline span announced as a polite live region. The new region
keeps `role="status"` + `aria-live="polite"` so the screen-reader behavior is
unchanged: a non-interruptive background read of the confirmation, not an
`alert`. (An error message is rendered in the same region with destructive
styling but the same polite live semantics — consistent with the prior inline
behavior, which used `role="status"` for both the ok and error kinds.)

### D4 — Auto-dismiss after a timeout; also clear eagerly on the next attempt. **(Confidence: medium — the spec left dismissal timing to the implementer)**

A persistent region needs a retirement story or a stale "Assigned 3 controls to
you." lingers across unrelated actions. Three dismissal triggers, picked for
the least operator surprise:

1. **Timeout.** `BULK_ASSIGN_MESSAGE_TTL_MS = 6000` (module-scope, greppable per
   the slice-227 `CONTROLS_PAGE_SIZE` precedent). A `useEffect` keyed on
   `assignMessage` arms a `window.setTimeout` to null the message and clears it
   on unmount / before the next message (`clearTimeout` in the cleanup). 6s is
   long enough to read a one-line confirmation, short enough not to outlive the
   operator's attention.
2. **Next attempt.** `onAssignToMe` already nulled the message before firing the
   request — so a fresh bulk-assign never shows the previous result. Unchanged;
   it now reads as deliberate dismissal-on-next-action.
3. **Implicit on error→success churn.** Each new `setAssignMessage` re-arms the
   timer (the effect re-runs), so the most recent message always gets a full TTL.

Not chosen: dismiss-on-next-selection-change. It would tie the confirmation's
lifetime back to selection state (the exact coupling D1 removed) and would
retire the message the instant the operator clicked a row to start the next
batch — too eager.

## What did NOT change (AC-5)

- No backend / API / authz change. `bulkAssignOwner`, `getMe`, the mutation
  wiring, and the `POST /v1/controls:bulk-assign-owner` round-trip are
  untouched. This is a pure render-location + dismissal-timing change.
- The pure-logic modules (`selection.ts`, `saved-views.ts`, `filters.ts`) and
  their node-env vitest suites are untouched.
- The saved-view-delete cache staleness (slice 746) is a separate finding and
  its quarantine is left in place.
