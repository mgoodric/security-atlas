# Issue #2090 — RowActions, inline confirm, neutral badges

A shared row overflow menu (`RowActions`) and inline confirm step
(`ConfirmInline`) in `web/components/ui/row-actions.tsx`, adopted on Admin ›
Features, API keys, Users and Super admins, and on Settings › API tokens. The
dashboard's mitigate pill and the Admin role badge leave the destructive tone.

- `detection_tier_actual`: none
- `detection_tier_target`: none

## D1 — One menu for rows and the detail header

**Decision.** `RowActionItem` keeps #2102's `OverflowItem` fields (`id`,
`label`, `destructive`, `confirmPrompt`, `confirmLabel`, `onSelect`) and adds
`confirmDetail`, `disabledReason` and `hidden`. The model
(`row-actions-model.ts`) is the same reducer shape as #2102's.

**Why.** #2102 (PR #2250, open at the time) built its overflow menu and
inline confirm ahead of this ticket and asked for one shared
implementation. Keeping its field names means the detail header can import
`RowActionItem` / `ConfirmInline` from here without changing its callers.

## D2 — base-ui Menu, inline confirm beside the trigger

**Decision.** The trigger is base-ui's `Menu` (`aria-haspopup="menu"`,
`aria-expanded`, arrow keys, Esc closes and returns focus). A destructive
item closes the menu and opens `ConfirmInline` under the trigger in the same
cell, focus on Cancel. Cancel, Esc or Confirm hands focus back to the
trigger. Never a native `confirm()` and never a modal.

## D3 — Disabled items carry their reason as text

**Decision.** An item with `disabledReason` renders disabled, with the reason
as a visible line inside the item (part of its accessible name) plus a
`title` tooltip.

**Why.** A hover tooltip alone is not read by screen readers, and the ticket
asks for the reason to be shown.

## D4 — Revoke roles and Demote keep their dialogs

**Decision.** On Users and Super admins the menu item opens the existing
confirm dialog instead of `ConfirmInline`.

**Why.** Both dialogs carry content that does not fit a table cell: the
user's still-live minted keys with their own per-key confirms (#1953), and
the 409 last-super-admin error surface (slice 142). They are already an
explicit confirm step; moving them inline would drop that content or crowd
the row. Flag disable and key/token revoke, which have a one-line
consequence, confirm inline.

## D5 — Flag disable confirms inline with its reason; enable keeps the modal

**Decision.** Disable is a destructive row-menu item whose inline confirm
holds the same optional reason field. Enable stays a button with its
existing modal and reason.

## D6 — Last-holder guards are presentation only

**Decision.** `components/admin/last-holder.ts` withholds "Revoke roles" from
a tenant's only active admin and "Demote" from the last listed super admin.
Nothing on the server changes.

**Why.** The ticket and the dispatcher brief: hiding or disabling is UI only,
and who can do what does not change. The super-admin 409 stays and is still
surfaced if a stale list lets a demote through (`super-admins.spec.ts`
covers that path with a two-row list). Two limits recorded:

- The users list is the first page only. When `next_cursor` is set, other
  admins may be on a later page, so nothing is withheld on a guess.
- `POST /v1/admin/users/revoke` does **not** refuse revoking a tenant's last
  admin today (only the group-role reconciler guards it,
  `internal/auth/grouprole/plan.go`). The UI guard is the only rail on that
  path; a server-side refusal belongs in a backend ticket, not here.

## D7 — Category tones

**Decision.** Dashboard mitigate pill: `destructive` → `progress`. Role badge
admin: `destructive` → `info`. The API-keys "admin" credential badge:
`destructive` → `secondary`. The register's treatment cell was already
neutral (`bg-muted`). Red is kept for irreversible actions.
