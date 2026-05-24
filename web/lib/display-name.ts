// Slice 213 — pure helpers for deriving the operator's display name +
// initials used by the topbar user-avatar component.
//
// Why pure helpers (and not a hook): the topbar's avatar component is
// rendered server-side (mirrors the slice 186 sidebar pattern — fetch
// /v1/me via the cookie jar on the server, render initials in markup
// the client receives whole). The string-derivation logic is hostile
// to test inside a server component, so it lives here, isolated and
// unit-testable.
//
// AC-4: "User avatar reads display name + initials from the existing
// user-context source (no new endpoint). Falls back to the email's
// local-part if `name` is unset."
//
// The fallback chain: display_name (trimmed) -> email local-part ->
// empty string. The component decides what to render when the value is
// empty (slice 213: render nothing).

/**
 * Minimal subset of the `/v1/me` profile-handler response shape this
 * module reads. Declared locally rather than importing from
 * `lib/api.ts` so the helper stays test-portable.
 */
export interface MeProfileFields {
  display_name?: string;
  email?: string;
}

/**
 * Derives the visible name string for the operator.
 *
 * Order:
 *   1. `display_name` (trimmed) if non-empty
 *   2. local-part of `email` (everything before the `@`)
 *   3. empty string
 */
export function deriveDisplayName(profile: MeProfileFields): string {
  const dn = (profile.display_name ?? "").trim();
  if (dn.length > 0) return dn;

  const email = (profile.email ?? "").trim();
  if (email.length === 0) return "";

  const at = email.indexOf("@");
  return at === -1 ? email : email.slice(0, at);
}

/**
 * Derives a 1-2 character initials string from a display name.
 *
 * Strategy: split on whitespace, drop empty tokens, take the first
 * letter of the first two tokens, uppercase. Returns empty string for
 * empty / whitespace-only input. Ignores leading non-letter characters
 * on each token (e.g. `"  Matt"` → `"M"`, not `"?"`).
 */
export function deriveInitials(displayName: string): string {
  const tokens = displayName.split(/\s+/).filter((t) => t.length > 0);
  if (tokens.length === 0) return "";

  const letters: string[] = [];
  for (const t of tokens) {
    // Pick the first letter character in the token. If none, skip the
    // token (so a token of all-punctuation does not become "?" or "-").
    const m = t.match(/[A-Za-z]/);
    if (m) letters.push(m[0]);
    if (letters.length === 2) break;
  }
  return letters.join("").toUpperCase();
}
