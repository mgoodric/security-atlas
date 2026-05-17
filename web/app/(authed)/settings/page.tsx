// Slice 103 -- /settings (user-facing only; admin lives at /admin).
//
// Per Plans/canvas/12-ui-fill-in-design-decisions.md section 4 (the SCOPE
// definition), this page is USER-facing only. Tenant-wide settings are
// at /admin/*. The page has five sections:
//
//   1. Profile -- read-only display from the session probe; the
//      backend has no GET /v1/me profile endpoint today, so the page
//      surfaces what is known (admin flag, OIDC subject placeholder)
//      and files a spillover slice for the real backend route.
//   2. Appearance -- theme picker (light / dark / system) persisted to
//      localStorage. No server-side theme sync in v1 (spillover).
//   3. Notifications -- per-event in-app + email toggles. No backend
//      preferences endpoint today, so toggles persist to localStorage
//      with a banner explaining the server roundtrip is pending
//      (spillover).
//   4. API tokens -- admin-only view that reuses the slice 062/063
//      /admin/api-keys plaintext-once flow. Non-admins see an
//      affordance pointing at /admin/api-keys; admin RBAC is enforced
//      at the backend (P0-A3).
//   5. Active sessions -- placeholder; no backend session-list
//      endpoint today (spillover).
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
import { useReducer, useState } from "react";

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
  getMe,
  getMyPreferences,
  getSessionMe,
  listMySessions,
  MePreferences,
  MeProfile,
  MeSession,
  patchMyPreferences,
  revokeMySession,
} from "@/lib/api";

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
              Tenant administration ({"->"}/admin)
            </a>
          ) : (
            <span className="text-muted-foreground">
              Tenant administration (admin role required)
            </span>
          )}
          .
        </p>
      </header>

      <ProfileSection isAdmin={isAdmin} loading={meQuery.isLoading} />
      <AppearanceSection />
      <NotificationsSection />
      <ApiTokensSection isAdmin={isAdmin} />
      <SessionsSection />
    </div>
  );
}

// --- Section 1: Profile ---------------------------------------------------

function ProfileSection({ isAdmin }: { isAdmin: boolean; loading: boolean }) {
  // Slice 108 wired GET /v1/me. Falls back to the credential-derived
  // tenant_role badge when the upstream returns a synthetic profile (API-key
  // bearer with no users row).
  const profileQuery = useQuery({
    queryKey: ["settings-me-profile"],
    queryFn: getMe,
  });
  const profile: MeProfile | undefined = profileQuery.data;
  return (
    <Card data-testid="settings-section-profile">
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
            </dd>
            <dt className="text-muted-foreground">Tenant role</dt>
            <dd className="col-span-2">
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
            </dd>
            <dt className="text-muted-foreground">OIDC subject</dt>
            <dd className="col-span-2">
              <code className="text-xs text-muted-foreground">
                {profile?.idp_subject || "(local user — no IdP backing)"}
              </code>
            </dd>
            <dt className="text-muted-foreground">Time zone</dt>
            <dd
              className="col-span-2 text-foreground"
              data-testid="settings-profile-time-zone"
            >
              {profile?.time_zone || (
                <span className="text-muted-foreground">(browser-derived)</span>
              )}
            </dd>
          </dl>
        )}
      </CardContent>
    </Card>
  );
}

// --- Section 2: Appearance ------------------------------------------------

const THEMES: { value: Theme; label: string; description: string }[] = [
  { value: "light", label: "Light", description: "Bright background" },
  { value: "dark", label: "Dark", description: "Low-light reading" },
  { value: "system", label: "System", description: "Follow OS preference" },
];

function AppearanceSection() {
  // The theme starts at DEFAULT_THEME during SSR (no localStorage on the
  // server). On mount, the AppearanceSelector child re-reads from
  // localStorage with a lazy initializer to avoid a hydration mismatch
  // while sidestepping the react-hooks/set-state-in-effect rule.
  return (
    <Card data-testid="settings-section-appearance">
      <CardHeader>
        <CardTitle>Appearance</CardTitle>
        <CardDescription>
          Theme preference is stored in your browser (no cross-device sync in
          this release).
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        <AppearanceSelector />
        <Alert>
          <AlertTitle>Dark-mode stylesheet pending</AlertTitle>
          <AlertDescription>
            Your selection is saved to <code>localStorage</code> and applied via
            the <code>data-theme</code> attribute on the root element. Visual
            dark-mode tokens land in a follow-up.
          </AlertDescription>
        </Alert>
      </CardContent>
    </Card>
  );
}

function AppearanceSelector() {
  // The selector mounts client-side only (parent is a "use client"
  // page). useState with a lazy initializer reads localStorage exactly
  // once on first render, sidestepping the
  // react-hooks/set-state-in-effect rule. The initializer is guarded so
  // SSR (no window) returns the default.
  const [theme, setTheme] = useState<Theme>(() => {
    if (typeof window === "undefined") return DEFAULT_THEME;
    return readTheme(window.localStorage);
  });

  function choose(next: Theme) {
    setTheme(next);
    if (typeof window !== "undefined") {
      writeTheme(window.localStorage, next);
      // Best-effort: set a data-theme attribute on <html> so any
      // future CSS that keys off it picks up the change immediately.
      // The v1 build does not ship dark-mode stylesheet tokens (see
      // banner above); the persistence is the contract.
      document.documentElement.setAttribute("data-theme", next);
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
      description: "When you are added as a sample reviewer on a period",
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
    <Card data-testid="settings-section-notifications">
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

  if (!isAdmin) {
    return (
      <Card data-testid="settings-section-tokens-non-admin">
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
    <Card data-testid="settings-section-tokens">
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
            bearer={freshSecret.bearer}
            last4={freshSecret.last4}
            issuedAt={freshSecret.issued_at}
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
              {list.data.map((c) => (
                <TableRow key={c.id} data-testid="settings-token-row">
                  <TableCell className="font-mono text-xs">{c.last4}</TableCell>
                  <TableCell className="text-xs">
                    {c.allowed_kinds.length === 0 ? (
                      <span className="text-muted-foreground">any</span>
                    ) : (
                      c.allowed_kinds.join(", ")
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
                    <Button
                      size="sm"
                      variant="destructive"
                      onClick={() => setRevokeConfirm(c)}
                      data-testid="settings-token-revoke-button"
                    >
                      Revoke
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
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
    </Card>
  );
}

function FreshTokenCallout({
  bearer,
  last4,
  issuedAt,
  onDismiss,
}: {
  bearer: string;
  last4: string;
  issuedAt: string;
  onDismiss: () => void;
}) {
  return (
    <Alert variant="destructive" data-testid="settings-fresh-token-callout">
      <AlertTitle>API token issued -- copy it now</AlertTitle>
      <AlertDescription className="space-y-2">
        <p className="font-medium">
          This is the only time you&apos;ll see this token. The platform does
          not store it in plaintext; if you lose it, issue a new one.
        </p>
        <div className="flex flex-col gap-2 sm:flex-row">
          <code
            className="flex-1 break-all rounded bg-foreground/5 p-2 font-mono text-xs"
            data-testid="settings-fresh-token-bearer"
          >
            {bearer}
          </code>
          <div className="flex gap-2">
            <Button
              variant="outline"
              size="sm"
              onClick={() => {
                if (typeof navigator !== "undefined") {
                  navigator.clipboard?.writeText(bearer);
                }
              }}
            >
              Copy
            </Button>
            <Button
              variant="outline"
              size="sm"
              onClick={onDismiss}
              data-testid="settings-fresh-token-dismiss"
            >
              Dismiss
            </Button>
          </div>
        </div>
        <p className="text-xs">
          Last 4: <code>{last4}</code> &middot; Issued at{" "}
          <code>{issuedAt}</code>
        </p>
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
    <Card data-testid="settings-section-sessions">
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
            {sessionsQuery.data.map((s: MeSession) => (
              <div
                key={s.id}
                className="flex items-center justify-between gap-3 rounded-md border border-border p-3 text-sm"
                data-testid="settings-session-row"
              >
                <div>
                  <div className="font-medium">
                    Session <code className="font-mono">…{s.last4}</code>
                  </div>
                  <div className="text-xs text-muted-foreground">
                    Created {s.created_at.slice(0, 10)}
                    {s.last_used_at
                      ? ` · last used ${s.last_used_at.slice(0, 10)}`
                      : null}
                  </div>
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
            ))}
          </div>
        )}
      </CardContent>
    </Card>
  );
}
