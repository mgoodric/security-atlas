// Slice 251 -- pure-logic helper: classify the Notifications-section
// render mode for the current caller's bearer.
//
// /v1/me/preferences (slice 108) is keyed on the users.id column. A
// credential bearer (bootstrap admin / API-key with no users row) makes
// the endpoint return `{error: "no preferences for this credential"}`
// (`internal/api/me/preferences.go:51,78`). The Notifications section
// must NOT surface that error string as the section's failure mode --
// it is the platform behaving correctly, not a bug. Instead the section
// renders an honest "this section is per-user; you are signed in as a
// credential" banner and skips rendering the four event rows.
//
// The detection signal is the synthetic-profile shape returned by
// `internal/api/me/profile.go:269-282`: a credential with no users-row
// backing comes back from GET /v1/me with:
//
//   - `idp_subject === ""`  (no OIDC subject -- the IdP never minted one)
//   - `email       === ""`  (no email -- the IdP never synced one)
//   - `display_name` of the form `"API key <last4>"`
//
// We treat **`idp_subject === ""`** as the canonical credential-bearer
// marker. The other two fields corroborate it but are downstream of the
// same backend branch -- using one signal keeps the helper tractable
// and avoids false positives when a future OIDC provider returns an
// empty email but a real subject (rare but documented).
//
// **Slice 250 de-dup landed.** Slice 250 (settings Profile section
// credential-bearer identity-leak) lifted the synthetic-profile detector
// to a shared module `./credential-bearer.ts`. This file's
// `isSyntheticCredentialProfile` is now a thin re-export of
// `isCredentialBearer(profile)` so the helper's public API stays
// byte-stable and existing callers do not change. The Notifications-
// section-specific `notificationsRenderMode` orchestration logic stays
// here -- it is the mode-resolution layer, not the detection layer.
//
// Pure function; vitest-covered in `notif-bearer-mode.test.ts`.
// No JSX, no fetch, no React import; safe to import from both the
// page and the test.

import type { MeProfile } from "@/lib/api/me";

import { isCredentialBearer } from "./credential-bearer";

/**
 * The Notifications section's render mode for the current bearer.
 *
 * - `"full"`        -- caller is a real user (OIDC-backed users row).
 *                      Render the four event rows × two channels.
 * - `"credential"`  -- caller is a credential bearer with no users row.
 *                      Render the honest-disclosure banner; skip the rows.
 * - `"loading"`     -- profile query has not resolved yet.
 *                      Render a skeleton (caller's existing branch).
 * - `"error"`       -- profile query errored before the section could
 *                      classify the bearer. The caller's existing
 *                      error branch handles this; the helper returns
 *                      this mode so the caller can short-circuit
 *                      cleanly.
 */
export type NotificationsRenderMode =
  | "full"
  | "credential"
  | "loading"
  | "error";

/**
 * Inputs derived from the Profile query (`GET /v1/me` via `getMe`) +
 * the optional preferences-query error. The preferences error is used
 * as a corroborating signal -- if `/v1/me/preferences` returns the
 * documented "no preferences for this credential" error AND the profile
 * looks synthetic, we are certain the bearer is a credential. The
 * profile signal alone is sufficient; the prefs error is a tie-breaker
 * for the (rare) case where the profile load succeeded but the prefs
 * load is the surface that fails first.
 */
export type NotificationsModeInput = {
  /** True while the profile query is in `isLoading`. */
  profileLoading: boolean;
  /** True if the profile query resolved with an error. */
  profileError: boolean;
  /** The MeProfile if it loaded; undefined while loading or on error. */
  profile:
    | Pick<MeProfile, "idp_subject" | "email" | "display_name">
    | undefined;
  /**
   * The message of the preferences query's error if one is present.
   * Empty string / undefined when the prefs query has not errored.
   * The handler compares case-insensitively against the documented
   * "no preferences for this credential" substring.
   */
  preferencesErrorMessage?: string;
};

/**
 * The exact error message string the platform returns for credential
 * bearers (`internal/api/me/preferences.go:51,78`). The check below is
 * substring-based + case-insensitive so a future BFF wrap (e.g.
 * `502: no preferences for this credential`) still matches.
 */
const CREDENTIAL_PREFS_ERROR_SUBSTRING = "no preferences for this credential";

/**
 * Decide how the NotificationsSection should render for the current
 * caller. Pure function; the caller passes the loading + data state
 * from its TanStack queries and the function maps them to a mode.
 *
 * Resolution order:
 *
 *   1. Profile still loading       -> `"loading"`.
 *   2. Profile errored             -> `"error"` (caller's existing
 *                                     error branch renders the alert).
 *   3. Profile synthetic-shape     -> `"credential"`.
 *   4. Preferences error matches   -> `"credential"` (corroborating
 *      "no preferences for this    signal; the profile shape might
 *      credential" substring)      have been misclassified upstream).
 *   5. Anything else               -> `"full"`.
 */
export function notificationsRenderMode(
  input: NotificationsModeInput,
): NotificationsRenderMode {
  if (input.profileLoading) return "loading";
  if (input.profileError) return "error";
  if (input.profile && isSyntheticCredentialProfile(input.profile)) {
    return "credential";
  }
  const prefsMsg = (input.preferencesErrorMessage ?? "").toLowerCase();
  if (prefsMsg.includes(CREDENTIAL_PREFS_ERROR_SUBSTRING)) {
    return "credential";
  }
  return "full";
}

/**
 * Whether the profile shape matches the synthetic-credential branch
 * (`internal/api/me/profile.go:269-282`).
 *
 * Slice 250 lifted the body to `./credential-bearer.ts#isCredentialBearer`.
 * This export is preserved as a thin wrapper so the existing slice-251
 * public API stays byte-stable for any external caller; new callers
 * should prefer `isCredentialBearer` directly.
 */
export function isSyntheticCredentialProfile(
  profile: Pick<MeProfile, "idp_subject" | "email" | "display_name">,
): boolean {
  return isCredentialBearer(profile);
}

/**
 * Operator-facing banner copy for the credential-bearer branch.
 *
 * Tone discipline (CLAUDE.md "Board-narrative AI-assist" -- the ban
 * list applies to operator-facing copy as well as board narratives):
 * measured, factual, slightly defensive. No "proud", "industry-
 * leading", "robust" filler. Explains WHAT the bearer sees + WHY it
 * is inert + HOW to manage notifications if needed.
 *
 * The string is exported so the Playwright spec can assert against the
 * exact wording without duplicating it in two places.
 */
export const CREDENTIAL_BEARER_BANNER_TITLE = "Notifications are per-user";

export const CREDENTIAL_BEARER_BANNER_BODY =
  "You are signed in as a credential (API key / bootstrap), which is not backed by a user account. Notification preferences are stored per user, so this section is inert for credential sign-ins. To manage notifications, sign in via your identity provider.";
