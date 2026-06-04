# 163 — Settings API tokens — Rotate action

**Cluster:** Frontend
**Estimate:** 0.5d
**Type:** JUDGMENT
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Surfaced during slice 154 (settings page audit), captured as follow-up
per continuous-batch policy.

`Plans/mockups/settings.html` Personal API Tokens table shows
`Rotate · Revoke` in the Actions column per row. Slice 062 shipped the
backend rotate path (`POST /v1/admin/credentials/:id/rotate` — returns
a successor credential row + new plaintext bearer). The slice 103
settings page Actions column today shows only "Revoke" — rotate was
not wired.

Slice 154's inline scope is <1h-per-finding corrections; rotate is a
~2-3h surface (reducer transition, second confirm modal, plaintext-once
re-application, rotated-from chain rendering, e2e). Filed as spillover.

**What this slice ships:**

- Add a new `ROTATED` action to the `web/app/(authed)/settings/
token-state.ts` reducer. Same plaintext-once invariant as ISSUED
  (the bearer is held in state for the duration of the callout and
  discarded on DISMISS or any subsequent ISSUED/ROTATED).
- New `RotateConfirmModal` mirroring `RevokeConfirmModal` but with
  copy explaining that the predecessor row stays visible with a
  "rotated" badge until separately revoked (slice 062 D-062-3
  precedent).
- New `useMutation` against `POST
/api/admin/credentials/:id/rotate` (BFF route is slice 060's
  existing surface; verify and extend if needed).
- Token list table renders a muted "rotated → …{last4}" link on
  predecessor rows (clickable to scroll to / highlight the
  successor row).
- `FreshTokenCallout` reused with copy varied to "rotated from
  …{predecessor_last4}" when entered via ROTATED path.

## Acceptance criteria

- [ ] AC-1: `token-state.ts` reducer gains `ROTATED` transition with
      `rotated_from: string` payload field; vitest covers the
      state-machine (ROTATED → DISMISS clears bearer; ROTATED →
      ISSUED clears predecessor bearer too).
- [ ] AC-2: Actions column shows `Rotate · Revoke` per row.
- [ ] AC-3: Rotate confirm modal opens; submit calls
      `rotateCred(id)`; success dispatches ROTATED + invalidates
      `["settings-creds"]` cache.
- [ ] AC-4: Predecessor row renders muted "rotated → …{last4}" badge
      when the list response carries `superseded_by` (slice 062 wire
      shape).
- [ ] AC-5: Plaintext-once invariant: rotate callout dismissed →
      bearer absent from DOM; reload → bearer absent from DOM
      (P0-A2 of slice 103 honored under the new path).
- [ ] AC-6: Playwright e2e asserts rotate-twice-in-a-row produces a
      fresh secret each time + chains the predecessor rows correctly.
- [ ] AC-7: CHANGELOG entry: "Personal API tokens: Rotate action on
      /settings (#163; closes slice 154 F8)".

## Dependencies

- **#062** Credential rotate backend (merged) — wires.
- **#103** Settings page (merged) — extends.
- **#154** Settings page audit (this PR, merged) — closes F8.

## Anti-criteria (P0 — block merge)

- **P0-163-1** Plaintext-once invariant for ROTATED bearer matches
  ISSUED: bearer never re-displays after DISMISS or after a second
  ROTATED/ISSUED.
- **P0-163-2** No new BFF route unless the existing
  `/api/admin/credentials/:id/rotate` is missing. Reuse first.
- **P0-163-3** NO change to slice 062 backend behavior or wire shape.
  This is a pure-frontend wiring slice.
- **P0-163-4** Vendor-prefixed test fixture tokens stay out (GitGuardian
  P0-A5 of slice 069).

## Notes for the implementing agent

Estimated 2-3 hours. JUDGMENT type because the predecessor-chain UX
(badge vs hidden vs duplicate-row) has design calls the engineer should
make and record.

Provenance: filed 2026-05-18 from slice 154 audit (F8).
