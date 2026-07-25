// Slice 103 -- /settings (user-facing only; admin lives at /admin).
// Slice 108 -- wired /v1/me, /v1/me/preferences, /v1/me/sessions.
// Slice 154 -- parity audit pass against Plans/_archive/mockups/settings.html:
//              section anchors (#profile / #appearance / ...), theme
//              swatches, multi-role tail badge, editable time-zone
//              picker bound to PATCH /v1/me, copy delta on the
//              audit-period assignment notification row, and the
//              Profile avatar / hero block. See
//              docs/audit-log/154-settings-page-audit-decisions.md for
//              the audit findings + the deferred items (sessions UA/IP
//              wire extension #162, rotate action #163, e2e seed
//              fixture #164).
// Slice 163 -- F8 spillover from slice 154 audit: wire the Rotate
//              action into the Personal API Tokens table. New
//              RotateConfirmModal mirrors RevokeConfirmModal; the
//              existing FreshTokenCallout is widened to render
//              rotate-flavour copy when entered via the ROTATED
//              reducer transition. Predecessor rows render a muted
//              "rotated -> ...last4" badge derived from the
//              SUCCESSOR's `rotated_from` field (slice 062 wire shape;
//              note the slice doc AC-4 mistakenly named the inverse
//              direction `superseded_by` -- see D3 in
//              docs/audit-log/163-settings-api-tokens-rotate-action-decisions.md).
//              Pure-frontend wiring -- no backend or BFF route change
//              (P0-163-2/P0-163-3).
//
// Per Plans/canvas/12-ui-fill-in-design-decisions.md section 4 (the
// SCOPE definition), this page is USER-facing only. Tenant-wide
// settings are at /admin/*. The page has five sections:
//
//   1. Profile -- GET /v1/me; PATCH /v1/me wires the time-zone editor
//      (display_name + IdP-managed email stay read-only per slice 108).
//   2. Appearance -- theme picker (light / dark / system) persisted to
//      localStorage. The visual swatch previews mirror the mockup; the
//      dark-mode stylesheet itself is still a follow-up (banner).
//   3. Notifications -- per-event in-app + email toggles backed by
//      GET/PATCH /v1/me/preferences (slice 108).
//   4. API tokens -- admin-only view that reuses the slice 062/063
//      /admin/api-keys plaintext-once flow. Non-admins see an
//      affordance pointing at /admin/api-keys; admin RBAC is enforced
//      at the backend (P0-A3). The mockup's Rotate action is
//      deferred to spillover slice #163.
//   5. Active sessions -- GET /v1/me/sessions + DELETE per-id (slice
//      108). The mockup's UA / IP / geo columns are a wire-shape
//      extension deferred to spillover slice #162.
//
// Cross-link "Tenant administration -> /admin" is visible only to
// admins (slice 097 D3 pattern: client-side via getSessionMe).
//
// P0 anti-criteria honored:
//   P0-A1: No tenant-wide settings on this page.
//   P0-A2: Bearer plaintext shown exactly once in callout; reducer
//          clears it on DISMISS or a second ISSUED.
//   P0-A3: Admin RBAC enforced upstream (/v1/admin/credentials
//          returns 403 for non-admin); UI cross-link visibility is
//          defense-in-depth.
//   P0-A4: No migration of /admin/* pages here.
//   P0-A5: No vendor-prefixed tokens in tests/fixtures.
//
// Slice 436 — this page was a 2152-line god-file holding every section
// plus its sub-components and BFF wrappers inline. The behavior-preserving
// split moved each section into its own `_tabs/` component; the page is now
// a thin composing shell. Same rendered UI, same testids, same routes —
// pure reorganization. See docs/audit-log/436-god-file-split-decisions.md.

"use client";

import { useQuery } from "@tanstack/react-query";

import { getSessionMe } from "@/lib/api/board";

import { ApiTokensSection } from "./_tabs/api-tokens";
import { AppearanceSection } from "./_tabs/appearance";
import { NotificationsSection } from "./_tabs/notifications";
import { ProfileSection } from "./_tabs/profile";
import { SessionsSection } from "./_tabs/sessions";
import { TenantSection } from "./_tabs/tenant";

export default function SettingsPage() {
  const meQuery = useQuery({
    queryKey: ["settings-session-me"],
    queryFn: getSessionMe,
  });
  const isAdmin = meQuery.data?.is_admin === true;

  return (
    <div className="mx-auto max-w-4xl space-y-6" data-testid="settings-page">
      <header>
        <h1 className="text-2xl font-semibold tracking-tight">Settings</h1>
        <p className="text-sm text-muted-foreground">
          Your personal preferences and credentials. Tenant-wide settings live
          in{" "}
          {isAdmin ? (
            <a
              href="/admin"
              className="text-primary underline-offset-4 hover:underline"
              data-testid="settings-admin-cross-link"
            >
              Tenant administration → /admin
            </a>
          ) : (
            <span className="text-muted-foreground">
              Tenant administration (admin role required)
            </span>
          )}
          .
        </p>
      </header>

      <ProfileSection isAdmin={isAdmin} />
      {isAdmin ? <TenantSection /> : null}
      <AppearanceSection />
      <NotificationsSection />
      <ApiTokensSection isAdmin={isAdmin} />
      <SessionsSection />
    </div>
  );
}
