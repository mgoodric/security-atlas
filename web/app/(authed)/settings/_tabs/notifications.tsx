"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";

import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";

import { Skeleton } from "@/components/ui/skeleton";

import {
  getEmailChannelOptIn,
  getMe,
  getMyPreferences,
  getSlackChannelOptIn,
  getWebhookChannelOptIn,
  MePreferences,
  patchMyPreferences,
  setEmailChannelOptIn,
  setSlackChannelOptIn,
  setWebhookChannelOptIn,
} from "@/lib/api/me";

import { isCredentialBearer } from "../credential-bearer";
import {
  CREDENTIAL_BEARER_BANNER_BODY,
  CREDENTIAL_BEARER_BANNER_TITLE,
  notificationsRenderMode,
} from "../notif-bearer-mode";

// --- Section 3: Notifications ---------------------------------------------

type NotifEvent =
  | "audit_period_assignment"
  | "policy_ack_due"
  | "risk_review_overdue"
  | "control_drift"
  // Slice 566: the two slice-445 digest kinds that previously had no per-kind
  // opt-out surface. Backed by the same slice-108 event whitelist (extended in
  // the same slice's migration + internal/auth/userprefs.Events).
  | "audit_note_reply"
  | "evidence_staleness";

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
    {
      key: "audit_note_reply",
      label: "Audit-note replies",
      description: "Replies on audit-note threads you're part of",
    },
    {
      key: "evidence_staleness",
      label: "Stale evidence",
      description: "Digest of evidence whose freshness window has lapsed",
    },
  ];

// Slice 108: notifications section is server-backed via GET/PATCH /v1/me/preferences.
// The localStorage fallback is retired; toggles update the server immediately and
// invalidate the cache to re-fetch.

export function NotificationsSection() {
  const qc = useQueryClient();
  // Slice 251: classify the caller's bearer to decide whether to render
  // the four event rows × two channels (OIDC user; existing behaviour)
  // or the honest-disclosure banner (credential bearer; new branch).
  // The profile query reuses the same query key as ProfileSection so
  // TanStack-Query dedupes -- no extra network round-trip. The prefs
  // query stays first-class because PATCH still flows through it for
  // the OIDC case; the credential branch never calls patchMut.
  const profileQuery = useQuery({
    queryKey: ["settings-me-profile"],
    queryFn: getMe,
  });
  const prefsQuery = useQuery({
    queryKey: ["settings-me-preferences"],
    queryFn: getMyPreferences,
    // Skip the prefs round-trip for credential bearers -- we already
    // know the platform returns the documented 404 + the section won't
    // render the rows. Saves a guaranteed-error fetch on every settings
    // page load for credential sign-ins. Slice 250 D2 / slice 251 D6
    // follow-up: the inline duplicate detection has been replaced with
    // the shared `isCredentialBearer` helper now that slice 250 lifted
    // it to `./credential-bearer.ts`.
    enabled:
      !profileQuery.isLoading &&
      !profileQuery.error &&
      !isCredentialBearer(profileQuery.data),
  });
  const patchMut = useMutation({
    mutationFn: patchMyPreferences,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["settings-me-preferences"] });
    },
  });
  // Slice 594: the per-kind slack/webhook columns reuse the slice-585
  // `configured` PRESENCE signal so a per-kind cell for an unconfigured
  // channel renders DISABLED (mirrors the master toggle's disabled state).
  // The query keys match the ChannelMasterToggle keys below, so TanStack
  // Query dedupes — no extra round-trip. While loading (data undefined) the
  // cell stays interactive (matches the master toggle's loading-tolerant
  // read: only an explicit configured===false disables). The OUTER runtime
  // gate is still the master opt-in (the 583 filter); the per-kind grid
  // mirrors the email column's independent-editability — a master-OFF state
  // does NOT disable the per-kind cell, only an unconfigured channel does.
  const slackChannelQuery = useQuery({
    queryKey: ["settings-slack-channel"],
    queryFn: getSlackChannelOptIn,
    enabled:
      !profileQuery.isLoading &&
      !profileQuery.error &&
      !isCredentialBearer(profileQuery.data),
  });
  const webhookChannelQuery = useQuery({
    queryKey: ["settings-webhook-channel"],
    queryFn: getWebhookChannelOptIn,
    enabled:
      !profileQuery.isLoading &&
      !profileQuery.error &&
      !isCredentialBearer(profileQuery.data),
  });
  const slackUnconfigured = slackChannelQuery.data?.configured === false;
  const webhookUnconfigured = webhookChannelQuery.data?.configured === false;
  const mode = notificationsRenderMode({
    profileLoading: profileQuery.isLoading,
    profileError: !!profileQuery.error,
    profile: profileQuery.data,
    preferencesErrorMessage:
      prefsQuery.error instanceof Error ? prefsQuery.error.message : undefined,
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
        {mode === "loading" || prefsQuery.isLoading ? (
          <Skeleton className="h-24 w-full" />
        ) : mode === "credential" ? (
          // Slice 251 D1: banner + skip rendering rows. Composes with
          // slice 250's credential-bearer detection -- when 250 lands,
          // de-dup the synthetic-profile check into a shared helper
          // (see notif-bearer-mode.ts header).
          <Alert data-testid="settings-notif-credential-banner">
            <AlertTitle data-testid="settings-notif-credential-banner-title">
              {CREDENTIAL_BEARER_BANNER_TITLE}
            </AlertTitle>
            <AlertDescription data-testid="settings-notif-credential-banner-body">
              {CREDENTIAL_BEARER_BANNER_BODY}
            </AlertDescription>
          </Alert>
        ) : mode === "error" || prefsQuery.error ? (
          <Alert variant="destructive">
            <AlertTitle>Could not load preferences</AlertTitle>
            <AlertDescription>
              {prefsQuery.error
                ? (prefsQuery.error as Error).message
                : (profileQuery.error as Error).message}
            </AlertDescription>
          </Alert>
        ) : (
          <div className="divide-y divide-border">
            {/* Slice 445: master email-channel opt-in (AC-9). Default
                opted-OUT (P0-445-7). Gates whether the per-event email
                column actually reaches the inbox. */}
            <EmailChannelMasterToggle />
            {/* Slice 584: master Slack + webhook opt-ins, mirroring the
                email toggle over the slice-543 routes. Default opted-OUT
                (P0-543-3). The channel target is OPERATOR-configured env,
                never user-supplied (P0-543-2 / SSRF) — these are opt-in
                booleans only, no URL/token field. */}
            <ChannelMasterToggle
              testidPrefix="settings-slack-channel"
              label="Slack delivery"
              description="Send a daily digest of your unread notifications to the operator-configured Slack channel. Off by default; the message carries summary counts and a link back into the app, never the notification details."
              queryKey="settings-slack-channel"
              getOptIn={getSlackChannelOptIn}
              setOptIn={setSlackChannelOptIn}
            />
            <ChannelMasterToggle
              testidPrefix="settings-webhook-channel"
              label="Webhook delivery"
              description="Send a daily digest of your unread notifications to the operator-configured webhook endpoint. Off by default; the payload carries summary counts and a link back into the app, never the notification details."
              queryKey="settings-webhook-channel"
              getOptIn={getWebhookChannelOptIn}
              setOptIn={setWebhookChannelOptIn}
            />
            {NOTIF_EVENTS.map((ev) => (
              <NotificationRow
                key={ev.key}
                event={ev}
                prefs={prefsQuery.data ?? {}}
                slackUnconfigured={slackUnconfigured}
                webhookUnconfigured={webhookUnconfigured}
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
  slackUnconfigured,
  webhookUnconfigured,
  onChange,
}: {
  event: { key: NotifEvent; label: string; description: string };
  prefs: MePreferences;
  // Slice 594: when the operator has not configured a channel (slice-585
  // configured===false), that channel's per-kind cell renders disabled —
  // setting a per-kind opt-out is meaningless if the channel can never
  // deliver. Defaults to false (interactive) so a missing/undefined
  // configured signal preserves the always-interactive behavior.
  slackUnconfigured?: boolean;
  webhookUnconfigured?: boolean;
  onChange: (
    channel: "in_app" | "email" | "slack" | "webhook",
    next: boolean,
  ) => void;
}) {
  // Server is the source of truth; defaults to true when the row is missing
  // (server-side default-on-missing-row policy in userprefs.Get). Slice 594
  // extends the same default-on-missing-row read to the slice-583 slack +
  // webhook channels (583 widened userprefs.Channels to admit them).
  const row = prefs[event.key] ?? {};
  const inApp = row.in_app !== false;
  const email = row.email !== false;
  const slack = row.slack !== false;
  const webhook = row.webhook !== false;
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
        <label className="flex items-center gap-1.5">
          <input
            type="checkbox"
            checked={slack}
            disabled={slackUnconfigured}
            onChange={(e) => onChange("slack", e.target.checked)}
            className="h-4 w-4"
            data-testid={`settings-notif-${event.key}-slack`}
          />
          Slack
        </label>
        <label className="flex items-center gap-1.5">
          <input
            type="checkbox"
            checked={webhook}
            disabled={webhookUnconfigured}
            onChange={(e) => onChange("webhook", e.target.checked)}
            className="h-4 w-4"
            data-testid={`settings-notif-${event.key}-webhook`}
          />
          webhook
        </label>
      </div>
    </div>
  );
}

// Slice 445: the master email-channel opt-in toggle. Default opted-OUT
// (P0-445-7); the operator opts in explicitly. Backed by GET/PUT
// /api/me/email-channel. Until this is on, no notification reaches the
// inbox regardless of the per-event email column.
function EmailChannelMasterToggle() {
  const qc = useQueryClient();
  const optInQuery = useQuery({
    queryKey: ["settings-email-channel"],
    queryFn: getEmailChannelOptIn,
  });
  const optInMut = useMutation({
    mutationFn: setEmailChannelOptIn,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["settings-email-channel"] });
    },
  });
  // Default to false while loading and on error (fail-closed display):
  // never imply the user is opted in when we are unsure.
  const enabled = optInQuery.data?.enabled === true;
  // Slice 585: disable + note when the operator has not configured SMTP
  // (configured === false). Missing/undefined is treated as configured.
  const unconfigured = optInQuery.data?.configured === false;
  return (
    <div
      className="flex items-start justify-between gap-3 py-3"
      data-testid="settings-email-channel-toggle-row"
    >
      <div>
        <div className="text-sm font-medium">Email delivery</div>
        <div className="text-xs text-muted-foreground">
          Send a daily digest of your unread notifications to your account
          email. Off by default; the email carries summary counts and a link
          back into the app, never the notification details.
        </div>
        {unconfigured && (
          <div
            className="mt-1 text-xs text-muted-foreground italic"
            data-testid="settings-email-channel-unconfigured-note"
          >
            This channel is not configured by your administrator.
          </div>
        )}
      </div>
      <label className="flex items-center gap-1.5 text-xs">
        <input
          type="checkbox"
          checked={enabled}
          disabled={optInQuery.isLoading || optInMut.isPending || unconfigured}
          onChange={(e) => optInMut.mutate(e.target.checked)}
          className="h-4 w-4"
          data-testid="settings-email-channel-toggle"
        />
        {enabled ? "on" : "off"}
      </label>
    </div>
  );
}

// Slice 584: a generic master channel opt-in toggle, parameterized over
// the channel's get/set fetchers. Mirrors EmailChannelMasterToggle exactly
// (same fail-closed display + disabled-while-pending shape) so the Slack +
// webhook rows behave identically to the email row. Default opted-OUT
// (P0-543-3). The channel target is operator-configured env, never
// user-supplied (P0-543-2) — this is a boolean toggle, not a URL field.
//
// Slice 585: when the GET wire reports `configured === false` (the operator
// has not set the channel's delivery env), the toggle renders DISABLED with
// a muted "not configured by your administrator" note. A missing/undefined
// `configured` is treated as configured (backward-tolerant — preserves the
// prior always-interactive behavior). `configured` is a boolean PRESENCE
// signal only; no secret target reaches the client (P0-585).
function ChannelMasterToggle({
  testidPrefix,
  label,
  description,
  queryKey,
  getOptIn,
  setOptIn,
}: {
  testidPrefix: string;
  label: string;
  description: string;
  queryKey: string;
  getOptIn: () => Promise<{ enabled: boolean; configured?: boolean }>;
  setOptIn: (
    enabled: boolean,
  ) => Promise<{ enabled: boolean; configured?: boolean }>;
}) {
  const qc = useQueryClient();
  const optInQuery = useQuery({
    queryKey: [queryKey],
    queryFn: getOptIn,
  });
  const optInMut = useMutation({
    mutationFn: setOptIn,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: [queryKey] });
    },
  });
  // Default to false while loading and on error (fail-closed display):
  // never imply the user is opted in when we are unsure.
  const enabled = optInQuery.data?.enabled === true;
  // Slice 585: only treat the channel as unconfigured once we have a
  // settled response that explicitly says configured === false. While
  // loading (data undefined), keep the toggle in its loading-disabled state
  // rather than flashing the unconfigured note.
  const unconfigured = optInQuery.data?.configured === false;
  return (
    <div
      className="flex items-start justify-between gap-3 py-3"
      data-testid={`${testidPrefix}-toggle-row`}
    >
      <div>
        <div className="text-sm font-medium">{label}</div>
        <div className="text-xs text-muted-foreground">{description}</div>
        {unconfigured && (
          <div
            className="mt-1 text-xs text-muted-foreground italic"
            data-testid={`${testidPrefix}-unconfigured-note`}
          >
            This channel is not configured by your administrator.
          </div>
        )}
      </div>
      <label className="flex items-center gap-1.5 text-xs">
        <input
          type="checkbox"
          checked={enabled}
          disabled={optInQuery.isLoading || optInMut.isPending || unconfigured}
          onChange={(e) => optInMut.mutate(e.target.checked)}
          className="h-4 w-4"
          data-testid={`${testidPrefix}-toggle`}
        />
        {enabled ? "on" : "off"}
      </label>
    </div>
  );
}
