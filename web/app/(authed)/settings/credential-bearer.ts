// Slice 250 -- shared credential-bearer detection for /settings.
//
// Background: the /settings page has three surfaces that need to render
// differently when the caller's JWT is bound to a credential (bootstrap
// admin / API-key with no users row) instead of an OIDC-backed human:
//
//   - Slice 251 -- Notifications section (banner; skip event rows).
//   - Slice 250 -- Profile section (banner; degrade IdP-specific copy;
//                  hide the time-zone editor since PATCH /v1/me 404s for
//                  credentials per `internal/api/me/profile.go:136`).
//   - Future       -- any other /me/* surface that returns a 404 for
//                     credentials (e.g. PATCH /v1/me; see slice 251 D6
//                     "Other /v1/me/* surfaces" follow-up).
//
// Slice 251 vendored the detection helper inline (`notif-bearer-mode.ts`
// `isSyntheticCredentialProfile`) and left a header comment flagging the
// de-dup with slice 250: "both should converge on a single shared
// `isCredentialBearer(profile)` predicate at
// `web/app/(authed)/settings/credential-bearer.ts`". Slice 250 fulfils
// that commitment -- this is the shared module. The slice 251 helper is
// re-exported from `notif-bearer-mode.ts` as a thin wrapper so
// `notif-bearer-mode.ts`'s public API stays byte-stable and existing
// callers do not change.
//
// Detection signal (unchanged from slice 251 D2): the synthetic-profile
// shape returned by `internal/api/me/profile.go:269-282` for credential
// bearers. The marker is `idp_subject === ""` AND `email === ""` -- a
// real OIDC user always carries a non-empty subject after sign-in. The
// `display_name` of the form `"API key <last4>"` is corroborating but
// NOT required; some future bootstrap path could emit a different
// display string without invalidating the predicate.
//
// Pure function; vitest-covered in `credential-bearer.test.ts`. No JSX,
// no fetch, no React import; safe to import from anywhere.

import type { MeProfile } from "@/lib/api";

/**
 * The Profile-shape inputs the detector needs. Pick<>'d so callers can
 * pass any object with those three fields without binding to the full
 * MeProfile type (eases test fixtures and prefetched-data variants).
 */
export type CredentialBearerCandidate = Pick<
  MeProfile,
  "idp_subject" | "email" | "display_name"
>;

/**
 * isCredentialBearer returns true when the profile is the synthetic
 * credential-bearer shape (`internal/api/me/profile.go:269-282`).
 *
 * The marker is two fields: `idp_subject` is empty AND `email` is empty.
 * Whitespace is treated as empty (defensive). A profile with an empty
 * subject but a non-empty email is treated as a real (mis-shaped) user --
 * the helper fails OPEN to the existing user-shaped rendering rather
 * than hiding a real user's affordances because of an upstream
 * wire-shape drift.
 *
 * Returns false for an undefined profile (caller's loading branch
 * handles that case).
 */
export function isCredentialBearer(
  profile: CredentialBearerCandidate | undefined,
): boolean {
  if (!profile) return false;
  const idpSubject = (profile.idp_subject ?? "").trim();
  if (idpSubject !== "") return false;
  const email = (profile.email ?? "").trim();
  if (email !== "") return false;
  return true;
}

/**
 * credentialDisplayLast4 extracts the trailing "<last4>" from a
 * synthetic `display_name` of the form `"API key <last4>"` so the UI
 * can render the bearer's last-4 identifier without re-displaying the
 * literal `"API key "` prefix. Returns the empty string when the shape
 * does not match -- the caller renders nothing or falls back to a
 * generic label.
 *
 * Examples:
 *   "API key 1f3a"   -> "1f3a"
 *   "API key "       -> ""  (degenerate; matches the live /v1/me sample
 *                            from the slice 250 spec)
 *   "Some other"     -> ""
 */
export function credentialDisplayLast4(
  displayName: string | undefined,
): string {
  const s = (displayName ?? "").trim();
  const prefix = "API key";
  if (!s.toLowerCase().startsWith(prefix.toLowerCase())) return "";
  const rest = s.slice(prefix.length).trim();
  // Expect 1-8 alphanumerics; reject longer / mixed-symbol payloads
  // defensively so a future "API key bootstrap-1f3a" does not silently
  // surface "bootstrap-1f3a" as a last4.
  if (!/^[A-Za-z0-9]{1,8}$/.test(rest)) return "";
  return rest;
}
