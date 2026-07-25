"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";

import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";

import { Skeleton } from "@/components/ui/skeleton";

import { getMe, MeProfile, patchMe } from "@/lib/api/me";

import {
  TIME_ZONE_OPTIONS,
  initialsFor,
  isCuratedTimeZone,
  tailRoles,
} from "../profile-derive";

import {
  PROFILE_CREDENTIAL_BANNER_BODY,
  PROFILE_CREDENTIAL_BANNER_TITLE,
  credentialBearerLabel,
} from "../profile-bearer-display";
import { isCredentialBearer } from "../credential-bearer";

// --- Section 1: Profile ---------------------------------------------------

export function ProfileSection({ isAdmin }: { isAdmin: boolean }) {
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
  //
  // Slice 250 (D1: Option 1 -- banner + degraded display): when /v1/me
  // returns the synthetic-credential shape (`isCredentialBearer`
  // === true), render an honesty banner above a *degraded* row layout:
  //   - The hero block's initials avatar is replaced with a generic
  //     "API key" label (the literal "API" or "AP" initials are
  //     meaningless for a credential).
  //   - The email row drops the "(read-only · managed by IdP)" caveat
  //     (credentials are not IdP-backed) and surfaces "(not applicable)".
  //   - The time-zone editor is hidden -- PATCH /v1/me would 404 for a
  //     credential bearer per `internal/api/me/profile.go:136`. Showing
  //     a control that 404s on submit is dishonest.
  //   - The tenant-role row stays unchanged (the role IS authoritative
  //     for the credential's authz).
  // The OIDC-human-user branch is byte-identical to the slice-154
  // rendering (P0-250-3, AC-3).
  const qc = useQueryClient();
  const profileQuery = useQuery({
    queryKey: ["settings-me-profile"],
    queryFn: getMe,
  });
  const profile: MeProfile | undefined = profileQuery.data;
  const isCredential = isCredentialBearer(profile);
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
          {isCredential
            ? "Describes the credential currently signed in. Personal profile fields require an identity-provider sign-in."
            : "Synced from your OIDC provider on sign-in. Display name and email are managed by your IdP."}
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
        ) : isCredential ? (
          <CredentialBearerProfile
            profile={profile as MeProfile}
            isAdmin={isAdmin}
          />
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

// CredentialBearerProfile renders the Profile section's degraded
// branch (slice 250 D1: Option 1). The OIDC-human-user branch above is
// preserved byte-identical -- this branch is additive.
//
// Layout differences vs. the OIDC-human-user branch:
//   - Banner (Alert) above the rows: title + body identifying the
//     bearer as a credential. Tone-discipline copy lives in
//     `./profile-bearer-display.ts`.
//   - Hero block: instead of two-letter initials, render a generic
//     "AK" badge (for API Key) + the `credentialBearerLabel` ("API key
//     …<last4>" or just "API key" for the degenerate sample).
//   - Email row: "(not applicable)" instead of "(unset)" + IdP caveat.
//   - Time-zone row: omitted entirely. PATCH /v1/me 404s for credentials
//     (see `internal/api/me/profile.go:136`); rendering an editor that
//     fails on submit would be dishonest.
//   - Tenant-role row: unchanged.
function CredentialBearerProfile({
  profile,
  isAdmin,
}: {
  profile: MeProfile;
  isAdmin: boolean;
}) {
  return (
    <>
      <Alert data-testid="settings-profile-credential-banner">
        <AlertTitle data-testid="settings-profile-credential-banner-title">
          {PROFILE_CREDENTIAL_BANNER_TITLE}
        </AlertTitle>
        <AlertDescription data-testid="settings-profile-credential-banner-body">
          {PROFILE_CREDENTIAL_BANNER_BODY}
        </AlertDescription>
      </Alert>
      <div
        className="mb-4 flex items-center gap-4"
        data-testid="settings-profile-hero"
      >
        <div
          className="flex h-14 w-14 items-center justify-center rounded-full bg-muted text-lg font-semibold text-muted-foreground"
          aria-hidden="true"
          data-testid="settings-profile-credential-badge"
        >
          AK
        </div>
        <div>
          <div
            className="text-sm font-medium text-foreground"
            data-testid="settings-profile-credential-label"
          >
            {credentialBearerLabel(profile.display_name)}
          </div>
          <div className="text-xs text-muted-foreground">
            Credential identifier &middot; not a user account
          </div>
        </div>
      </div>
      <dl className="grid grid-cols-3 gap-x-4 gap-y-3 text-sm">
        <dt className="text-muted-foreground">Display name</dt>
        <dd
          className="col-span-2 text-foreground"
          data-testid="settings-profile-display-name"
        >
          {credentialBearerLabel(profile.display_name)}
          <span className="ml-2 text-xs text-muted-foreground">
            (credential identifier)
          </span>
        </dd>
        <dt className="text-muted-foreground">Email</dt>
        <dd
          className="col-span-2 text-muted-foreground"
          data-testid="settings-profile-credential-email"
        >
          (not applicable &middot; credentials are not backed by a user)
        </dd>
        <dt className="text-muted-foreground">Tenant role</dt>
        <dd
          className="col-span-2 flex flex-wrap items-center gap-1.5"
          data-testid="settings-profile-roles"
        >
          {isAdmin ? (
            <Badge data-testid="settings-profile-role-admin">admin</Badge>
          ) : (
            <Badge variant="outline" data-testid="settings-profile-role-user">
              user
            </Badge>
          )}
          <RolesTail roles={profile.roles} isAdmin={isAdmin} />
        </dd>
        {/*
         * Time-zone editor intentionally omitted for credential bearers:
         * PATCH /v1/me 404s for credentials (internal/api/me/profile.go:136).
         * The picker is hidden rather than shown-disabled because there is
         * nothing to set -- the credential's time_zone field is always null
         * on the wire.
         */}
      </dl>
    </>
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
