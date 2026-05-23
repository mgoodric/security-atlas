// Slice 103 -- /settings (user-facing only; admin lives at /admin).
// Slice 108 -- wired /v1/me, /v1/me/preferences, /v1/me/sessions.
// Slice 154 -- parity audit pass against Plans/mockups/settings.html:
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

"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useReducer, useState } from "react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  AdminCredential,
  AdminCredentialIssueRequest,
  AdminCredentialIssueResponse,
  AdminCredentialListResponse,
  AdminCredentialRotateResponse,
  getMe,
  getMyPreferences,
  getSessionMe,
  listMySessions,
  MePreferences,
  MeProfile,
  MeSession,
  patchMe,
  patchMyPreferences,
  revokeMySession,
} from "@/lib/api";
import { applyThemeClass } from "@/lib/theme-class";

import {
  TIME_ZONE_OPTIONS,
  initialsFor,
  isCuratedTimeZone,
  tailRoles,
} from "./profile-derive";
import { isAnyKind, kindsLabel } from "./allowed-kinds-display";
import { sessionLine } from "./session-line";
import { DEFAULT_THEME, readTheme, Theme, writeTheme } from "./theme";
import { initialState, reduce } from "./token-state";

// --- BFF wrappers ---------------------------------------------------------

async function fetchCreds(): Promise<AdminCredential[]> {
  const res = await fetch(`/api/admin/credentials`);
  if (res.status === 403) {
    // Non-admin -- surfaced to the section via the empty-array path.
    return [];
  }
  if (!res.ok) {
    throw new Error(`list credentials: ${res.status}`);
  }
  const body = (await res.json()) as AdminCredentialListResponse;
  return body.items ?? [];
}

async function issueCred(
  body: AdminCredentialIssueRequest,
): Promise<AdminCredentialIssueResponse> {
  const res = await fetch(`/api/admin/credentials`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!res.ok) {
    throw new Error(`issue credential: ${res.status}`);
  }
  return (await res.json()) as AdminCredentialIssueResponse;
}

async function revokeCred(id: string): Promise<void> {
  const res = await fetch(
    `/api/admin/credentials/${encodeURIComponent(id)}/revoke`,
    { method: "POST" },
  );
  if (!res.ok) {
    throw new Error(`revoke credential: ${res.status}`);
  }
}

// Slice 163: rotateCred mirrors revokeCred but hits the
// already-shipped /api/admin/credentials/:id/rotate BFF route (slice
// 060). The successor's bearer plaintext is returned ONCE and is the
// caller's only chance to capture it -- the reducer holds it for the
// duration of the callout, then DISMISS clears it.
async function rotateCred(id: string): Promise<AdminCredentialRotateResponse> {
  const res = await fetch(
    `/api/admin/credentials/${encodeURIComponent(id)}/rotate`,
    { method: "POST" },
  );
  if (!res.ok) {
    throw new Error(`rotate credential: ${res.status}`);
  }
  return (await res.json()) as AdminCredentialRotateResponse;
}

// --- Page -----------------------------------------------------------------

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

// --- Section 1: Profile ---------------------------------------------------

function ProfileSection({ isAdmin }: { isAdmin: boolean }) {
  // Slice 108 wired GET /v1/me. Falls back to the credential-derived
  // tenant_role badge when the upstream returns a synthetic profile (API-key
  // bearer with no users row).
  //
  // Slice 154:
  //   - Avatar / hero block above the dl rows (initialsFor helper).
  //   - Tenant Role line shows the multi-role tail (slice 130 `roles`).
  //   - Time zone is an editable <select> bound to PATCH /v1/me; the
  //     curated nine-zone list in `profile-derive.ts` covers the v1
  //     primary-user persona. Zones outside the list still render
  //     correctly when the backend reports them (synthetic option).
  const qc = useQueryClient();
  const profileQuery = useQuery({
    queryKey: ["settings-me-profile"],
    queryFn: getMe,
  });
  const profile: MeProfile | undefined = profileQuery.data;
  const tzMut = useMutation({
    mutationFn: (time_zone: string) => patchMe({ time_zone }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["settings-me-profile"] });
    },
  });
  return (
    <Card id="profile" data-testid="settings-section-profile">
      <CardHeader>
        <CardTitle>Profile</CardTitle>
        <CardDescription>
          Synced from your OIDC provider on sign-in. Display name and email are
          managed by your IdP.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        {profileQuery.isLoading ? (
          <Skeleton className="h-24 w-full" />
        ) : profileQuery.error ? (
          <Alert variant="destructive">
            <AlertTitle>Could not load profile</AlertTitle>
            <AlertDescription>
              {(profileQuery.error as Error).message}
            </AlertDescription>
          </Alert>
        ) : (
          <>
            <div
              className="mb-4 flex items-center gap-4"
              data-testid="settings-profile-hero"
            >
              <div
                className="flex h-14 w-14 items-center justify-center rounded-full bg-primary/10 text-lg font-semibold text-primary"
                aria-hidden="true"
                data-testid="settings-profile-initials"
              >
                {initialsFor({
                  display_name: profile?.display_name ?? "",
                  email: profile?.email ?? "",
                })}
              </div>
              <div>
                <div className="text-sm font-medium text-foreground">
                  {profile?.display_name || (
                    <span className="text-muted-foreground">(unset)</span>
                  )}
                </div>
                <div className="text-xs text-muted-foreground">
                  {profile?.email || "(no email synced)"}
                  {profile?.idp_subject ? (
                    <>
                      {" "}
                      &middot; OIDC subject{" "}
                      <code className="font-mono text-[11px]">
                        {profile.idp_subject}
                      </code>
                    </>
                  ) : null}
                </div>
              </div>
            </div>
            <dl className="grid grid-cols-3 gap-x-4 gap-y-3 text-sm">
              <dt className="text-muted-foreground">Display name</dt>
              <dd
                className="col-span-2 text-foreground"
                data-testid="settings-profile-display-name"
              >
                {profile?.display_name || (
                  <span className="text-muted-foreground">(unset)</span>
                )}
              </dd>
              <dt className="text-muted-foreground">Email</dt>
              <dd className="col-span-2 text-foreground">
                {profile?.email || (
                  <span className="text-muted-foreground">(unset)</span>
                )}
                <span className="ml-2 text-xs text-muted-foreground">
                  (read-only · managed by IdP)
                </span>
              </dd>
              <dt className="text-muted-foreground">Tenant role</dt>
              <dd
                className="col-span-2 flex flex-wrap items-center gap-1.5"
                data-testid="settings-profile-roles"
              >
                {isAdmin ? (
                  <Badge data-testid="settings-profile-role-admin">admin</Badge>
                ) : (
                  <Badge
                    variant="outline"
                    data-testid="settings-profile-role-user"
                  >
                    user
                  </Badge>
                )}
                <RolesTail roles={profile?.roles} isAdmin={isAdmin} />
              </dd>
              <dt className="text-muted-foreground">Time zone</dt>
              <dd
                className="col-span-2"
                data-testid="settings-profile-time-zone"
              >
                <TimeZonePicker
                  value={profile?.time_zone ?? null}
                  pending={tzMut.isPending}
                  onChange={(next) => tzMut.mutate(next)}
                />
                {tzMut.error ? (
                  <p
                    className="mt-1 text-xs text-destructive"
                    data-testid="settings-profile-time-zone-error"
                  >
                    {(tzMut.error as Error).message}
                  </p>
                ) : null}
              </dd>
            </dl>
          </>
        )}
      </CardContent>
    </Card>
  );
}

// RolesTail renders the slice-130 `roles` list as the muted
// "+ grc_engineer + auditor" tail next to the primary admin/user badge.
// Returns nothing when there are no secondary roles to show.
function RolesTail({
  roles,
  isAdmin,
}: {
  roles: string[] | undefined;
  isAdmin: boolean;
}) {
  const tail = tailRoles(roles, isAdmin);
  if (tail.length === 0) return null;
  return (
    <span
      className="text-xs text-muted-foreground"
      data-testid="settings-profile-roles-tail"
    >
      + {tail.join(" + ")}
    </span>
  );
}

// TimeZonePicker renders a styled <select> bound to PATCH /v1/me. When
// the current value is outside the curated list, the select prepends an
// "out-of-band" option so the user still sees their zone honestly. A
// blank value (server reports `time_zone === null`) selects the empty
// option which is labeled "(browser-derived)".
function TimeZonePicker({
  value,
  pending,
  onChange,
}: {
  value: string | null;
  pending: boolean;
  onChange: (next: string) => void;
}) {
  const showOutOfBand =
    value !== null && value !== "" && !isCuratedTimeZone(value);
  return (
    <select
      className="rounded-md border border-border bg-background px-2 py-1 text-sm focus:outline-none focus:ring-2 focus:ring-ring disabled:opacity-50"
      value={value ?? ""}
      disabled={pending}
      onChange={(e) => onChange(e.target.value)}
      data-testid="settings-profile-time-zone-select"
      aria-label="Time zone"
    >
      <option value="">(browser-derived)</option>
      {showOutOfBand ? <option value={value as string}>{value}</option> : null}
      {TIME_ZONE_OPTIONS.map((z) => (
        <option key={z} value={z}>
          {z}
        </option>
      ))}
    </select>
  );
}

// --- Section 2: Appearance ------------------------------------------------

// Slice 154: each theme option carries a swatch preview class (the
// mockup shows a 48-px-tall card-shaped preview above the label so the
// user picks visually instead of reading three descriptions). The
// `swatch` class is a Tailwind utility composition — no new components
// added (Article VIII Anti-Abstraction).
const THEMES: {
  value: Theme;
  label: string;
  description: string;
  swatch: string;
}[] = [
  {
    value: "light",
    label: "Light",
    description: "Bright background",
    swatch: "bg-white border border-border",
  },
  {
    value: "dark",
    label: "Dark",
    description: "Low-light reading",
    swatch: "bg-slate-900 border border-slate-700",
  },
  {
    value: "system",
    label: "System",
    description: "Follow OS preference",
    swatch: "bg-gradient-to-br from-white to-slate-900 border border-border",
  },
];

// --- Section 1.5: Tenant (admin/super_admin only) -------------------------
//
// Slice 144: rename-tenant flow. Admins see a tenant-name input field
// + Save button. Renders only when the caller holds the admin role on
// the CURRENT tenant (per slice 097 D3 pattern: client-side via
// getSessionMe + an upstream-enforced 403 from the platform). The
// platform's authority gate is the canonical guard; the
// hide-when-not-admin is UX-only and not load-bearing.
//
// The section reads the current tenant via `GET /v1/me/tenants` (the
// slice 192 BFF route already shipped) and PATCHes via the slice 144
// BFF route `/api/tenants/[id]`. Errors map 1:1 to the wire response:
// 409 (duplicate name) renders an inline conflict notice; 400 renders
// the upstream error message.

type MeTenantRow = {
  id: string;
  name: string;
  current: boolean;
};

type MeTenantsResponse = {
  tenants: MeTenantRow[];
};

async function fetchMyTenants(): Promise<MeTenantsResponse> {
  const res = await fetch(`/api/me/tenants`, { cache: "no-store" });
  if (!res.ok) {
    throw new Error(`list my tenants: ${res.status}`);
  }
  return (await res.json()) as MeTenantsResponse;
}

async function patchTenantName(
  id: string,
  name: string,
): Promise<{ tenant: { name: string } }> {
  const res = await fetch(`/api/tenants/${encodeURIComponent(id)}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ name }),
  });
  if (!res.ok) {
    const body = await res.text();
    let parsed: { error?: string } = {};
    try {
      parsed = JSON.parse(body) as { error?: string };
    } catch {
      // body might be plaintext; fall through
    }
    const err = new Error(
      parsed.error ?? `rename tenant: ${res.status}`,
    ) as Error & {
      status?: number;
    };
    err.status = res.status;
    throw err;
  }
  return (await res.json()) as { tenant: { name: string } };
}

function TenantSection() {
  const qc = useQueryClient();
  const tenantsQuery = useQuery({
    queryKey: ["settings-my-tenants"],
    queryFn: fetchMyTenants,
  });
  const currentTenant = tenantsQuery.data?.tenants.find((t) => t.current);
  const [draft, setDraft] = useState<string>("");
  const [conflict, setConflict] = useState<string | null>(null);
  const [success, setSuccess] = useState<string | null>(null);

  // Seed the draft from the loaded current tenant exactly once after
  // the query resolves. The draft is intentionally allowed to diverge
  // from `currentTenant.name` afterwards so the user can edit without
  // a re-fetch resetting their input. Same post-mount-sync pattern as
  // AppearanceSelector (slice 170 D1) — syncing from a non-React
  // state source (TanStack Query cache) into local component state
  // is the canonical case for the disabled rule.
  useEffect(() => {
    if (currentTenant && draft === "") {
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setDraft(currentTenant.name);
    }
  }, [currentTenant, draft]);

  const renameMut = useMutation({
    mutationFn: (next: string) =>
      patchTenantName(currentTenant?.id ?? "", next),
    onSuccess: (resp) => {
      setConflict(null);
      setSuccess(`Renamed to "${resp.tenant.name}".`);
      qc.invalidateQueries({ queryKey: ["settings-my-tenants"] });
      // Also invalidate the slice 192 switcher cache.
      qc.invalidateQueries({ queryKey: ["tenant-switcher"] });
    },
    onError: (err: Error & { status?: number }) => {
      setSuccess(null);
      if (err.status === 409) {
        setConflict(
          "Another tenant already uses that name. Pick a different one.",
        );
      } else if (err.status === 403) {
        setConflict("You do not have permission to rename this tenant.");
      } else {
        setConflict(err.message);
      }
    },
  });

  const disabled =
    !currentTenant ||
    renameMut.isPending ||
    draft.trim() === "" ||
    draft.trim() === currentTenant?.name;

  return (
    <Card id="tenant" data-testid="settings-section-tenant">
      <CardHeader>
        <CardTitle>Tenant</CardTitle>
        <CardDescription>
          Rename your current tenant. The new name shows up immediately in the
          tenant switcher for everyone on this tenant.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        {tenantsQuery.isLoading ? (
          <Skeleton className="h-10 w-full" />
        ) : tenantsQuery.error ? (
          <Alert variant="destructive">
            <AlertTitle>Could not load tenant</AlertTitle>
            <AlertDescription>
              {(tenantsQuery.error as Error).message}
            </AlertDescription>
          </Alert>
        ) : !currentTenant ? (
          <Alert>
            <AlertTitle>No current tenant</AlertTitle>
            <AlertDescription>
              You are not currently scoped to a tenant. Sign back in or pick a
              tenant from the switcher first.
            </AlertDescription>
          </Alert>
        ) : (
          <>
            <dl className="grid grid-cols-3 gap-x-4 gap-y-3 text-sm">
              <dt className="text-muted-foreground">Tenant ID</dt>
              <dd
                className="col-span-2 font-mono text-xs text-foreground"
                data-testid="settings-tenant-id"
              >
                {currentTenant.id}
              </dd>
              <dt className="text-muted-foreground">Name</dt>
              <dd className="col-span-2">
                <Input
                  value={draft}
                  onChange={(e) => {
                    setDraft(e.target.value);
                    setConflict(null);
                    setSuccess(null);
                  }}
                  maxLength={64}
                  aria-label="Tenant name"
                  data-testid="settings-tenant-name-input"
                />
              </dd>
            </dl>
            <div className="flex items-center gap-3">
              <Button
                type="button"
                onClick={() => renameMut.mutate(draft.trim())}
                disabled={disabled}
                data-testid="settings-tenant-save-btn"
              >
                {renameMut.isPending ? "Saving…" : "Save name"}
              </Button>
              <p className="text-xs text-muted-foreground">
                Up to 64 bytes. Names are case-insensitive unique across the
                deployment.
              </p>
            </div>
            {conflict ? (
              <Alert variant="destructive">
                <AlertDescription data-testid="settings-tenant-error">
                  {conflict}
                </AlertDescription>
              </Alert>
            ) : null}
            {success ? (
              <Alert>
                <AlertDescription data-testid="settings-tenant-success">
                  {success}
                </AlertDescription>
              </Alert>
            ) : null}
          </>
        )}
      </CardContent>
    </Card>
  );
}

function AppearanceSection() {
  // The theme starts at DEFAULT_THEME during SSR (no localStorage on the
  // server). On mount, the AppearanceSelector child re-reads from
  // localStorage with a lazy initializer to avoid a hydration mismatch
  // while sidestepping the react-hooks/set-state-in-effect rule.
  return (
    <Card id="appearance" data-testid="settings-section-appearance">
      <CardHeader>
        <CardTitle>Appearance</CardTitle>
        <CardDescription>
          Theme preference is stored in your browser (no cross-device sync in
          this release).
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        <AppearanceSelector />
        {/*
         * Slice 203 — the slice-170 "Dark-mode stylesheet pending" banner is
         * retired. The class wire is live: selecting Dark or System (with
         * OS=dark) now activates the `.dark { ... }` token block in
         * globals.css; data-theme stays written for ThemeAwareLogo
         * compatibility.
         */}
      </CardContent>
    </Card>
  );
}

function AppearanceSelector() {
  // Slice 170 D1 (Pattern A: useEffect post-mount sync) — the prior
  // implementation used `useState` with an SSR-guarded lazy initializer.
  // That initializer runs exactly once per server-or-client render PASS,
  // and React reuses the server-rendered state on hydration: the client
  // never re-ran the initializer, so `localStorage` was never consulted
  // on a fresh page load. Result: the picker always booted to
  // `DEFAULT_THEME` regardless of the user's persisted choice. The fix:
  // seed state with `DEFAULT_THEME` (matching the SSR pass for
  // hydration-mismatch safety per AC-2) and read `localStorage` in a
  // single-shot `useEffect` after mount. The post-mount setState causes
  // a one-frame flicker from "system" to the stored value; per slice 170
  // P0-A5 / Notes-for-Implementing-Agent, that's acceptable below the
  // fold. See docs/audit-log/170-settings-theme-picker-hydration-decisions.md.
  const [theme, setTheme] = useState<Theme>(DEFAULT_THEME);
  useEffect(() => {
    // Post-mount synchronization from a non-React state source
    // (localStorage) is the canonical pattern for this scenario; see
    // react.dev "synchronizing with external systems". The set-state runs
    // exactly once on mount and seeds the picker from the persisted
    // choice. Removing this would re-introduce the slice 170 hydration
    // bug. The react-hooks/set-state-in-effect rule is intentionally
    // disabled on the next line.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setTheme(readTheme(window.localStorage));
  }, []);

  function choose(next: Theme) {
    setTheme(next);
    if (typeof window !== "undefined") {
      writeTheme(window.localStorage, next);
      // Slice 170 contract: set `data-theme` on <html> -- the
      // ThemeAwareLogo (slice 176) keys off this attribute, and any
      // future consumer that prefers attribute-based reads stays
      // wire-compatible. P0-A1 (slice 203): data-theme remains the
      // source-of-truth; the class below is in ADDITION, not a
      // replacement.
      document.documentElement.setAttribute("data-theme", next);
      // Slice 203: write the `dark` class so the Tailwind v4 custom
      // variant `@custom-variant dark (&:is(.dark *))` configured in
      // `web/app/globals.css:5` matches and the `.dark { ... }` token
      // block at globals.css:86+ activates. Without this, the picker
      // persists a choice but the page never themes (the slice-170
      // deferred-work banner).
      const prefersDark = window.matchMedia(
        "(prefers-color-scheme: dark)",
      ).matches;
      applyThemeClass(document.documentElement, next, prefersDark);
    }
  }

  return (
    <div
      className="grid max-w-md grid-cols-3 gap-3"
      role="radiogroup"
      aria-label="Theme"
    >
      {THEMES.map((opt) => {
        const selected = theme === opt.value;
        return (
          <button
            key={opt.value}
            type="button"
            role="radio"
            aria-checked={selected}
            onClick={() => choose(opt.value)}
            data-testid={`settings-theme-option-${opt.value}`}
            data-selected={selected ? "true" : "false"}
            className={
              selected
                ? "rounded-md border-2 border-primary bg-primary/5 p-3 text-left"
                : "rounded-md border border-border bg-background p-3 text-left hover:border-foreground/40"
            }
          >
            <div
              className={`mb-2 h-12 rounded ${opt.swatch}`}
              aria-hidden="true"
              data-testid={`settings-theme-swatch-${opt.value}`}
            />
            <div className="text-sm font-medium">{opt.label}</div>
            <div className="text-xs text-muted-foreground">
              {opt.description}
            </div>
          </button>
        );
      })}
    </div>
  );
}

// --- Section 3: Notifications ---------------------------------------------

type NotifEvent =
  | "audit_period_assignment"
  | "policy_ack_due"
  | "risk_review_overdue"
  | "control_drift";

const NOTIF_EVENTS: { key: NotifEvent; label: string; description: string }[] =
  [
    {
      key: "audit_period_assignment",
      label: "Audit-period assignments",
      // Slice 154 F5: the "in-progress" qualifier is load-bearing —
      // assignment notifications fire only for open periods, not for
      // historical periods or refreshes (slice 108 D-108-2). Restore
      // the mockup copy so the user is not surprised by a stale
      // assignment fire on a frozen period.
      description:
        "When you're added as a sample reviewer on an in-progress period",
    },
    {
      key: "policy_ack_due",
      label: "Policy acknowledgment due",
      description: "When a policy requiring your role publishes a new version",
    },
    {
      key: "risk_review_overdue",
      label: "Risk review overdue",
      description: "Risks you own that pass their review_due_at",
    },
    {
      key: "control_drift",
      label: "Control drift",
      description: "Controls you own that flip pass to fail",
    },
  ];

// Slice 108: notifications section is server-backed via GET/PATCH /v1/me/preferences.
// The localStorage fallback is retired; toggles update the server immediately and
// invalidate the cache to re-fetch.

function NotificationsSection() {
  const qc = useQueryClient();
  const prefsQuery = useQuery({
    queryKey: ["settings-me-preferences"],
    queryFn: getMyPreferences,
  });
  const patchMut = useMutation({
    mutationFn: patchMyPreferences,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["settings-me-preferences"] });
    },
  });
  return (
    <Card id="notifications" data-testid="settings-section-notifications">
      <CardHeader>
        <CardTitle>Notifications</CardTitle>
        <CardDescription>
          Routing for items the platform assigns to you.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        {prefsQuery.isLoading ? (
          <Skeleton className="h-24 w-full" />
        ) : prefsQuery.error ? (
          <Alert variant="destructive">
            <AlertTitle>Could not load preferences</AlertTitle>
            <AlertDescription>
              {(prefsQuery.error as Error).message}
            </AlertDescription>
          </Alert>
        ) : (
          <div className="divide-y divide-border">
            {NOTIF_EVENTS.map((ev) => (
              <NotificationRow
                key={ev.key}
                event={ev}
                prefs={prefsQuery.data ?? {}}
                onChange={(channel, next) =>
                  patchMut.mutate({ [ev.key]: { [channel]: next } })
                }
              />
            ))}
          </div>
        )}
      </CardContent>
    </Card>
  );
}

function NotificationRow({
  event,
  prefs,
  onChange,
}: {
  event: { key: NotifEvent; label: string; description: string };
  prefs: MePreferences;
  onChange: (channel: "in_app" | "email", next: boolean) => void;
}) {
  // Server is the source of truth; defaults to true when the row is missing
  // (server-side default-on-missing-row policy in userprefs.Get).
  const row = prefs[event.key] ?? {};
  const inApp = row.in_app !== false;
  const email = row.email !== false;
  return (
    <div
      className="flex items-start justify-between gap-3 py-3"
      data-testid={`settings-notif-row-${event.key}`}
    >
      <div>
        <div className="text-sm font-medium">{event.label}</div>
        <div className="text-xs text-muted-foreground">{event.description}</div>
      </div>
      <div className="flex items-center gap-3 text-xs">
        <label className="flex items-center gap-1.5">
          <input
            type="checkbox"
            checked={inApp}
            onChange={(e) => onChange("in_app", e.target.checked)}
            className="h-4 w-4"
            data-testid={`settings-notif-${event.key}-in-app`}
          />
          in-app
        </label>
        <label className="flex items-center gap-1.5">
          <input
            type="checkbox"
            checked={email}
            onChange={(e) => onChange("email", e.target.checked)}
            className="h-4 w-4"
            data-testid={`settings-notif-${event.key}-email`}
          />
          email
        </label>
      </div>
    </div>
  );
}

// --- Section 4: API tokens ------------------------------------------------

function ApiTokensSection({ isAdmin }: { isAdmin: boolean }) {
  const qc = useQueryClient();
  const list = useQuery({
    queryKey: ["settings-creds"],
    queryFn: fetchCreds,
    enabled: isAdmin,
  });
  const [freshSecret, dispatch] = useReducer(reduce, initialState);
  const [issueOpen, setIssueOpen] = useState(false);
  const [revokeConfirm, setRevokeConfirm] = useState<AdminCredential | null>(
    null,
  );
  // Slice 163: a second confirm modal for Rotate. Same shape as
  // revokeConfirm -- when set, the modal renders for that credential
  // and the modal's onConfirm fires the rotateMut.
  const [rotateConfirm, setRotateConfirm] = useState<AdminCredential | null>(
    null,
  );

  const issueMut = useMutation({
    mutationFn: issueCred,
    onSuccess: (out) => {
      dispatch({
        kind: "ISSUED",
        bearer: out.bearer_token,
        last4: out.last4,
        issued_at: out.issued_at,
      });
      setIssueOpen(false);
      qc.invalidateQueries({ queryKey: ["settings-creds"] });
    },
  });

  const revokeMut = useMutation({
    mutationFn: revokeCred,
    onSuccess: () => {
      setRevokeConfirm(null);
      qc.invalidateQueries({ queryKey: ["settings-creds"] });
    },
  });

  // Slice 163: rotateMut dispatches ROTATED on success. The predecessor's
  // last4 is captured from the modal's row at click-time (passed as the
  // mutation variable) so the callout can render "rotated from ...XXXX"
  // without re-querying the list. The bearer plaintext flows through
  // state ONCE and is GC'd on DISMISS (P0-163-1).
  const rotateMut = useMutation({
    mutationFn: (args: { id: string; predecessor_last4: string }) =>
      rotateCred(args.id),
    onSuccess: (out, args) => {
      dispatch({
        kind: "ROTATED",
        bearer: out.bearer_token,
        last4: out.last4,
        predecessor_last4: args.predecessor_last4,
        predecessor_expires_at: out.predecessor_expires_at,
      });
      setRotateConfirm(null);
      qc.invalidateQueries({ queryKey: ["settings-creds"] });
    },
  });

  // Slice 163: derive the predecessor -> successor link map from the
  // list. The slice 062 wire shape carries `rotated_from` on the
  // SUCCESSOR; to surface the forward direction on a predecessor row's
  // badge ("rotated -> ...succ") we invert -- for each row with a
  // rotated_from, the row pointed-to-by-rotated_from has THIS row as
  // its successor. Memoised on list.data so the inversion does not
  // re-run on unrelated re-renders (modal open/close, mutation
  // pending-state flips).
  const successorByPredecessorId = useMemo(() => {
    const m = new Map<string, { id: string; last4: string }>();
    for (const c of list.data ?? []) {
      if (c.rotated_from) {
        m.set(c.rotated_from, { id: c.id, last4: c.last4 });
      }
    }
    return m;
  }, [list.data]);

  if (!isAdmin) {
    return (
      <Card id="tokens" data-testid="settings-section-tokens-non-admin">
        <CardHeader>
          <CardTitle>Personal API tokens</CardTitle>
          <CardDescription>
            For CLI use (<code>security-atlas evidence push</code>).
          </CardDescription>
        </CardHeader>
        <CardContent>
          <Alert>
            <AlertTitle>Admin role required</AlertTitle>
            <AlertDescription>
              Issuing personal API tokens currently requires the{" "}
              <strong>admin</strong> role. Contact your tenant administrator, or
              visit{" "}
              <a href="/admin/api-keys" className="underline">
                /admin/api-keys
              </a>{" "}
              if you have admin access in another session. User-scoped token
              issuance is a follow-up slice.
            </AlertDescription>
          </Alert>
        </CardContent>
      </Card>
    );
  }

  return (
    <Card id="tokens" data-testid="settings-section-tokens">
      <CardHeader>
        <div className="flex items-center justify-between gap-3">
          <div>
            <CardTitle>Personal API tokens</CardTitle>
            <CardDescription>
              For CLI use (<code>security-atlas evidence push</code>). Token
              last-4 shown; plaintext never re-displayed.
            </CardDescription>
          </div>
          <Button
            size="sm"
            onClick={() => setIssueOpen(true)}
            data-testid="settings-token-issue-button"
          >
            Issue token
          </Button>
        </div>
      </CardHeader>
      <CardContent className="space-y-4">
        {freshSecret.kind === "issued" ? (
          <FreshTokenCallout
            variant="issued"
            bearer={freshSecret.bearer}
            last4={freshSecret.last4}
            issuedAt={freshSecret.issued_at}
            onDismiss={() => dispatch({ kind: "DISMISS" })}
          />
        ) : freshSecret.kind === "rotated" ? (
          <FreshTokenCallout
            variant="rotated"
            bearer={freshSecret.bearer}
            last4={freshSecret.last4}
            predecessorLast4={freshSecret.predecessor_last4}
            predecessorExpiresAt={freshSecret.predecessor_expires_at}
            onDismiss={() => dispatch({ kind: "DISMISS" })}
          />
        ) : null}

        {issueOpen ? (
          <IssueTokenForm
            submitting={issueMut.isPending}
            onCancel={() => setIssueOpen(false)}
            onSubmit={(body) => issueMut.mutate(body)}
          />
        ) : null}

        {list.isLoading ? (
          <Skeleton
            className="h-32 w-full"
            data-testid="settings-tokens-loading"
          />
        ) : list.error ? (
          <Alert variant="destructive">
            <AlertTitle>Could not load tokens</AlertTitle>
            <AlertDescription>{(list.error as Error).message}</AlertDescription>
          </Alert>
        ) : list.data && list.data.length > 0 ? (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-24">Last 4</TableHead>
                <TableHead>Allowed kinds</TableHead>
                <TableHead>Scope</TableHead>
                <TableHead className="w-32">Issued</TableHead>
                <TableHead className="w-32">Last used</TableHead>
                <TableHead className="w-32 text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {list.data.map((c) => {
                // Slice 163: row id is prefixed with `token-row-` so the
                // predecessor badge's `href="#token-row-{successor.id}"`
                // cannot collide with an unrelated element on the page.
                const rowAnchor = `token-row-${c.id}`;
                const successor = successorByPredecessorId.get(c.id);
                return (
                  <TableRow
                    key={c.id}
                    id={rowAnchor}
                    data-testid="settings-token-row"
                  >
                    <TableCell className="font-mono text-xs">
                      {c.last4}
                      {successor ? (
                        <a
                          href={`#token-row-${successor.id}`}
                          className="ml-2 inline-flex items-center rounded bg-muted px-1.5 py-0.5 text-[10px] font-normal text-muted-foreground hover:bg-muted-foreground/10"
                          data-testid="settings-token-rotated-to-link"
                          title={`Rotated to successor ending in ${successor.last4}`}
                        >
                          rotated {"->"} …{successor.last4}
                        </a>
                      ) : null}
                    </TableCell>
                    <TableCell className="text-xs">
                      {isAnyKind(c.allowed_kinds) ? (
                        <span className="text-muted-foreground">any</span>
                      ) : (
                        kindsLabel(c.allowed_kinds)
                      )}
                    </TableCell>
                    <TableCell className="font-mono text-[10px] text-muted-foreground">
                      {c.scope_predicate || "{}"}
                    </TableCell>
                    <TableCell className="font-mono text-xs">
                      {c.issued_at.slice(0, 10)}
                    </TableCell>
                    <TableCell className="font-mono text-xs">
                      {c.last_used_at ? (
                        c.last_used_at.slice(0, 10)
                      ) : (
                        <span className="text-muted-foreground">never</span>
                      )}
                    </TableCell>
                    <TableCell className="text-right">
                      <div className="flex justify-end gap-2">
                        <Button
                          size="sm"
                          variant="outline"
                          onClick={() => setRotateConfirm(c)}
                          data-testid="settings-token-rotate-button"
                        >
                          Rotate
                        </Button>
                        <Button
                          size="sm"
                          variant="destructive"
                          onClick={() => setRevokeConfirm(c)}
                          data-testid="settings-token-revoke-button"
                        >
                          Revoke
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        ) : (
          <p className="py-6 text-center text-sm text-muted-foreground">
            No active tokens. Click <strong>Issue token</strong> above to mint
            one.
          </p>
        )}
      </CardContent>

      {revokeConfirm ? (
        <RevokeConfirmModal
          cred={revokeConfirm}
          submitting={revokeMut.isPending}
          onCancel={() => setRevokeConfirm(null)}
          onConfirm={() => revokeMut.mutate(revokeConfirm.id)}
        />
      ) : null}

      {rotateConfirm ? (
        <RotateConfirmModal
          cred={rotateConfirm}
          submitting={rotateMut.isPending}
          onCancel={() => setRotateConfirm(null)}
          onConfirm={() =>
            rotateMut.mutate({
              id: rotateConfirm.id,
              predecessor_last4: rotateConfirm.last4,
            })
          }
        />
      ) : null}
    </Card>
  );
}

// Slice 103 / Slice 163 -- one-shot plaintext callout used by both the
// ISSUED and ROTATED reducer paths. The variant prop selects the copy
// (title, "issued at" vs "rotated -- predecessor retires at") without
// duplicating the surrounding callout chrome. The bearer flows in as a
// string and is rendered into a <code> element inside the callout's
// JSX -- the moment the parent component dispatches DISMISS, the
// callout unmounts and the bearer reference goes out of scope. There
// is no DOM persistence across re-renders, no localStorage write, no
// hidden duplicate element.
type FreshTokenCalloutProps =
  | {
      variant: "issued";
      bearer: string;
      last4: string;
      issuedAt: string;
      onDismiss: () => void;
    }
  | {
      variant: "rotated";
      bearer: string;
      last4: string;
      predecessorLast4: string;
      predecessorExpiresAt: string;
      onDismiss: () => void;
    };

function FreshTokenCallout(props: FreshTokenCalloutProps) {
  const title =
    props.variant === "issued"
      ? "API token issued -- copy it now"
      : "API token rotated -- copy the new bearer now";
  const helperParagraph =
    props.variant === "issued"
      ? "This is the only time you'll see this token. The platform does not store it in plaintext; if you lose it, issue a new one."
      : `This is the only time you'll see this token. The predecessor ending in ${props.predecessorLast4} keeps working until the timestamp below; rotate again or revoke it once your clients have switched over.`;
  return (
    <Alert variant="destructive" data-testid="settings-fresh-token-callout">
      <AlertTitle data-testid="settings-fresh-token-title">{title}</AlertTitle>
      <AlertDescription className="space-y-2">
        <p className="font-medium">{helperParagraph}</p>
        <div className="flex flex-col gap-2 sm:flex-row">
          <code
            className="flex-1 break-all rounded bg-foreground/5 p-2 font-mono text-xs"
            data-testid="settings-fresh-token-bearer"
          >
            {props.bearer}
          </code>
          <div className="flex gap-2">
            <Button
              variant="outline"
              size="sm"
              onClick={() => {
                if (typeof navigator !== "undefined") {
                  navigator.clipboard?.writeText(props.bearer);
                }
              }}
            >
              Copy
            </Button>
            <Button
              variant="outline"
              size="sm"
              onClick={props.onDismiss}
              data-testid="settings-fresh-token-dismiss"
            >
              Dismiss
            </Button>
          </div>
        </div>
        {props.variant === "issued" ? (
          <p className="text-xs">
            Last 4: <code>{props.last4}</code> &middot; Issued at{" "}
            <code>{props.issuedAt}</code>
          </p>
        ) : (
          <p
            className="text-xs"
            data-testid="settings-fresh-token-rotated-meta"
          >
            Successor last 4: <code>{props.last4}</code> &middot; Rotated from{" "}
            <code>…{props.predecessorLast4}</code> &middot; Predecessor retires
            at <code>{props.predecessorExpiresAt}</code>
          </p>
        )}
      </AlertDescription>
    </Alert>
  );
}

function IssueTokenForm({
  submitting,
  onCancel,
  onSubmit,
}: {
  submitting: boolean;
  onCancel: () => void;
  onSubmit: (body: AdminCredentialIssueRequest) => void;
}) {
  const [scopePredicate, setScopePredicate] = useState("");
  const [allowedKinds, setAllowedKinds] = useState("");
  const [ttlDays, setTtlDays] = useState(90);

  function submit() {
    const kinds = allowedKinds
      .split(",")
      .map((s) => s.trim())
      .filter(Boolean);
    onSubmit({
      scope_predicate: scopePredicate.trim() || "{}",
      allowed_kinds: kinds,
      ttl_seconds: ttlDays * 86400,
      is_admin: false,
      is_approver: false,
      owner_roles: [],
    });
  }

  return (
    <Card data-testid="settings-token-issue-form">
      <CardHeader>
        <CardTitle className="text-base">Issue a new personal token</CardTitle>
        <CardDescription>
          Scope narrows which evidence kinds and scope cells the bearer can push
          to. TTL is in days.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        <div className="grid gap-3 sm:grid-cols-2">
          <label className="space-y-1.5 text-sm">
            <span className="font-medium">Scope predicate (JSON)</span>
            <Input
              value={scopePredicate}
              onChange={(e) => setScopePredicate(e.target.value)}
              placeholder='{"connector":"aws"}'
            />
          </label>
          <label className="space-y-1.5 text-sm">
            <span className="font-medium">Allowed kinds (comma-separated)</span>
            <Input
              value={allowedKinds}
              onChange={(e) => setAllowedKinds(e.target.value)}
              placeholder="aws.s3.encryption.v1"
            />
          </label>
          <label className="space-y-1.5 text-sm">
            <span className="font-medium">TTL (days)</span>
            <Input
              type="number"
              min={1}
              max={3650}
              value={ttlDays}
              onChange={(e) => setTtlDays(Number(e.target.value))}
            />
          </label>
        </div>
        <div className="flex justify-end gap-2">
          <Button variant="outline" onClick={onCancel} disabled={submitting}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={submitting}>
            {submitting ? "Issuing..." : "Issue token"}
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}

function RevokeConfirmModal({
  cred,
  submitting,
  onCancel,
  onConfirm,
}: {
  cred: AdminCredential;
  submitting: boolean;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  return (
    <div
      role="dialog"
      aria-modal="true"
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4"
      data-testid="settings-token-revoke-modal"
    >
      <Card className="w-full max-w-md">
        <CardHeader>
          <CardTitle>Revoke token?</CardTitle>
          <CardDescription>
            Last 4 <code>{cred.last4}</code>
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-3 text-sm">
          <p>
            Revocation is immediate. Any client using this bearer will start
            failing on its next call. Last seen:{" "}
            <code>{cred.last_used_at ?? "never"}</code>.
          </p>
          <div className="flex justify-end gap-2">
            <Button variant="outline" onClick={onCancel} disabled={submitting}>
              Cancel
            </Button>
            <Button
              variant="destructive"
              onClick={onConfirm}
              disabled={submitting}
            >
              {submitting ? "Revoking..." : "Revoke now"}
            </Button>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}

// Slice 163: RotateConfirmModal is a parallel of RevokeConfirmModal
// with rotate-specific copy. Rotation produces a NEW plaintext bearer
// for the successor row; the predecessor row stays visible with a
// muted "rotated -> ...last4" badge until the user separately revokes
// it (slice 062 D-062-3). The modal copy makes this explicit so the
// user is not surprised by the predecessor row sticking around.
function RotateConfirmModal({
  cred,
  submitting,
  onCancel,
  onConfirm,
}: {
  cred: AdminCredential;
  submitting: boolean;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  return (
    <div
      role="dialog"
      aria-modal="true"
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4"
      data-testid="settings-token-rotate-modal"
    >
      <Card className="w-full max-w-md">
        <CardHeader>
          <CardTitle>Rotate token?</CardTitle>
          <CardDescription>
            Last 4 <code>{cred.last4}</code>
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-3 text-sm">
          <p>
            Rotation mints a successor with the same scope and allowed kinds,
            and returns a fresh bearer plaintext. The predecessor keeps working
            for a short grace window so clients can switch over -- it stays
            visible in this list with a muted &ldquo;rotated&rdquo; badge until
            you revoke it.
          </p>
          <p>
            You&apos;ll see the new bearer EXACTLY ONCE. Have a place to paste
            it before continuing.
          </p>
          <div className="flex justify-end gap-2">
            <Button variant="outline" onClick={onCancel} disabled={submitting}>
              Cancel
            </Button>
            <Button onClick={onConfirm} disabled={submitting}>
              {submitting ? "Rotating..." : "Rotate now"}
            </Button>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}

// --- Section 5: Active sessions ------------------------------------------

// Slice 108: sessions section is server-backed via GET /v1/me/sessions +
// DELETE /v1/me/sessions/{id}. The "current" flag depends on the atlas_session
// cookie reaching the platform; bearer-only requests (no cookie) leave every
// row unflagged — surfaced via an explanatory tooltip rather than a banner so
// the section UI matches the design.

function SessionsSection() {
  const qc = useQueryClient();
  const sessionsQuery = useQuery({
    queryKey: ["settings-me-sessions"],
    queryFn: listMySessions,
  });
  const revokeMut = useMutation({
    mutationFn: revokeMySession,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["settings-me-sessions"] });
    },
  });
  return (
    <Card id="sessions" data-testid="settings-section-sessions">
      <CardHeader>
        <CardTitle>Active sessions</CardTitle>
        <CardDescription>Browsers currently signed in as you.</CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        {sessionsQuery.isLoading ? (
          <Skeleton className="h-24 w-full" />
        ) : sessionsQuery.error ? (
          <Alert variant="destructive">
            <AlertTitle>Could not load sessions</AlertTitle>
            <AlertDescription>
              {(sessionsQuery.error as Error).message}
            </AlertDescription>
          </Alert>
        ) : !sessionsQuery.data || sessionsQuery.data.length === 0 ? (
          <p
            className="py-6 text-center text-sm text-muted-foreground"
            data-testid="settings-sessions-empty"
          >
            No active OIDC sessions. Sessions appear here after sign-in via your
            IdP.
          </p>
        ) : (
          <div className="space-y-2">
            {sessionsQuery.data.map((s: MeSession) => {
              // Slice 162: build the augmented session line (UA · IP · geo).
              // sessionLine() returns "" when none of the fields are present,
              // so we conditionally render the second line to keep pre-
              // migration rows visually unchanged (P0-162-1: no fabrication).
              const metaLine = sessionLine(s);
              return (
                <div
                  key={s.id}
                  className="flex items-center justify-between gap-3 rounded-md border border-border p-3 text-sm"
                  data-testid="settings-session-row"
                >
                  <div className="min-w-0">
                    <div className="font-medium">
                      Session <code className="font-mono">…{s.last4}</code>
                    </div>
                    <div className="text-xs text-muted-foreground">
                      Created {s.created_at.slice(0, 10)}
                      {s.last_used_at
                        ? ` · last used ${s.last_used_at.slice(0, 10)}`
                        : null}
                    </div>
                    {metaLine !== "" ? (
                      <div
                        className="mt-0.5 truncate text-xs text-muted-foreground"
                        data-testid="settings-session-meta"
                        title={s.user_agent ?? undefined}
                      >
                        {metaLine}
                      </div>
                    ) : null}
                  </div>
                  <div className="flex items-center gap-2">
                    {s.is_current ? (
                      <Badge variant="outline">current</Badge>
                    ) : (
                      <Button
                        size="sm"
                        variant="destructive"
                        onClick={() => revokeMut.mutate(s.id)}
                        disabled={revokeMut.isPending}
                        data-testid="settings-session-revoke"
                      >
                        Revoke
                      </Button>
                    )}
                  </div>
                </div>
              );
            })}
          </div>
        )}
      </CardContent>
    </Card>
  );
}
