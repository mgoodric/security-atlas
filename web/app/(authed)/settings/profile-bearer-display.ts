// Slice 250 -- Profile-section copy + display helpers for the
// credential-bearer branch.
//
// The detection predicate lives in `./credential-bearer.ts`
// (`isCredentialBearer`). This file owns the operator-facing strings
// the ProfileSection renders WHEN the predicate returns true:
//
//   1. The banner title + body that identifies the bearer as a
//      credential (not a person) -- mirrors slice 251's
//      `CREDENTIAL_BEARER_BANNER_TITLE` / `CREDENTIAL_BEARER_BANNER_BODY`
//      pattern but tuned to the Profile section's framing (where the
//      page is about "your account").
//   2. `credentialBearerLabel` -- the human-readable label rendered in
//      the hero block in place of an initials avatar. Composes with
//      `credentialDisplayLast4` so a bearer with last4 "1f3a" renders
//      as "API key …1f3a" and the degenerate live sample (display_name
//      = "API key ") renders as "API key".
//
// Tone discipline (CLAUDE.md "Board-narrative AI-assist" ban list
// applies to all operator-facing copy): measured, factual, slightly
// defensive. No "proud", "industry-leading", "robust" filler.

import { credentialDisplayLast4 } from "./credential-bearer";

/**
 * Banner title shown above the Profile section when the caller is a
 * credential bearer. Matches the slice-251 phrasing pattern -- short,
 * identifies the surface as different from the OIDC-human-user shape.
 */
export const PROFILE_CREDENTIAL_BANNER_TITLE =
  "You are signed in as a credential";

/**
 * Banner body. Two sentences: WHAT the bearer is + WHY the fields below
 * are about the credential (not a person) + HOW to see a personal
 * profile.
 *
 * The body MUST NOT echo the platform's raw "API key " display_name with
 * the trailing space, MUST NOT include marketing tone, and MUST NOT
 * invent fields that do not exist on /v1/me (P0-250-1).
 */
export const PROFILE_CREDENTIAL_BANNER_BODY =
  "This page describes the credential (API key or bootstrap admin), not a person. The fields below report the credential's metadata. For your personal profile, sign in via your identity provider.";

/**
 * credentialBearerLabel returns the human-readable label to render
 * in the hero block in place of the initials avatar.
 *
 * Examples:
 *   "API key 1f3a" -> "API key …1f3a"
 *   "API key "     -> "API key"            (degenerate live sample)
 *   "bootstrap"    -> "API key"            (defensive fallback)
 *   undefined      -> "API key"
 */
export function credentialBearerLabel(displayName: string | undefined): string {
  const last4 = credentialDisplayLast4(displayName);
  if (last4 === "") return "API key";
  return `API key …${last4}`;
}
