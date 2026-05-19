// Slice 154 — pure-logic helpers for the Profile section of /settings.
//
// Two helpers live here:
//   - `initialsFor(profile)` derives a two-letter avatar string from the
//     profile shape (display_name → first letters of first two words;
//     fallback to email local-part; fallback to "??"). Mirrors the
//     `Plans/mockups/settings.html` profile avatar.
//   - `tailRoles(roles, isAdmin)` filters the slice-130 `roles` array to
//     the secondary roles that should render as the "+ grc_engineer"
//     muted tail next to the primary admin/user badge. Excludes the
//     role already covered by `is_admin` (admin) and the implicit "user"
//     pseudo-role.
//
// Both are pure functions — vitest-covered in `profile-derive.test.ts`.
// No JSX, no fetch, no localStorage; this file is safe to import from
// both the page and the test.

import type { MeProfile } from "@/lib/api";

/**
 * IANA time zones offered in the Profile section's time-zone picker.
 *
 * Curated to nine zones sized for the v1 primary-user persona (Bay Area
 * security startup) plus its most common customer geos. The platform
 * accepts the full IANA list (~600 zones) via `time.LoadLocation`, but a
 * `<select>` of 600 entries is unusable. Slice 154 deferred the
 * autocomplete-Combobox to a future slice; until then, zones outside
 * this list cannot be set from the UI (the API still accepts them).
 */
export const TIME_ZONE_OPTIONS: readonly string[] = [
  "America/Los_Angeles",
  "America/Denver",
  "America/Chicago",
  "America/New_York",
  "Europe/London",
  "Europe/Berlin",
  "Asia/Singapore",
  "Asia/Tokyo",
  "UTC",
] as const;

/**
 * initialsFor derives a two-letter avatar string for the Profile section.
 *
 * Resolution order:
 *   1. `display_name` — first letter of first two whitespace-split words.
 *      Single-word names fall through to step 2 via the email path.
 *   2. `email` — first two letters of the local-part (pre-`@`).
 *   3. literal "??" — fallback when both are empty (synthetic API-key
 *      profile with no users row backing).
 *
 * Output is uppercased ASCII. Non-letter characters are skipped so that
 * a `display_name = "(unset)"` does not produce "(U".
 */
export function initialsFor(
  profile: Pick<MeProfile, "display_name" | "email">,
): string {
  const name = (profile.display_name ?? "").trim();
  if (name.length > 0) {
    const words = name.split(/\s+/).filter((w) => w.length > 0);
    if (words.length >= 2) {
      const a = firstLetter(words[0]);
      const b = firstLetter(words[1]);
      if (a && b) return (a + b).toUpperCase();
    }
    // Single word — take first two letters of the word itself, if any.
    if (words.length === 1) {
      const letters = words[0].replace(/[^A-Za-z]/g, "");
      if (letters.length >= 2) return letters.slice(0, 2).toUpperCase();
      if (letters.length === 1) {
        // Fall through to the email path for the second letter.
        const emailSecond = firstLetter(
          (profile.email ?? "").split("@")[0] ?? "",
        );
        if (emailSecond) return (letters + emailSecond).toUpperCase();
      }
    }
  }
  const local = (profile.email ?? "").split("@")[0] ?? "";
  const letters = local.replace(/[^A-Za-z]/g, "");
  if (letters.length >= 2) return letters.slice(0, 2).toUpperCase();
  if (letters.length === 1) return (letters + "?").toUpperCase();
  return "??";
}

function firstLetter(s: string): string | null {
  for (const ch of s) {
    if (/[A-Za-z]/.test(ch)) return ch;
  }
  return null;
}

/**
 * tailRoles filters the slice-130 `roles` array to the secondary roles
 * that should render as the muted "+ grc_engineer" tail next to the
 * primary admin/user badge.
 *
 * Drops:
 *   - "admin" when `isAdmin === true` (already shown via the primary badge).
 *   - "user" (implicit pseudo-role; the primary badge already covers it).
 *   - duplicates (defensive — the wire is supposed to be deduped).
 *
 * Returns roles in input order so the wire's ordering (typically the DB
 * `user_roles` insertion order) is preserved. Caller renders the result
 * comma-joined; an empty array means no tail.
 */
export function tailRoles(
  roles: readonly string[] | undefined,
  isAdmin: boolean,
): string[] {
  if (!roles || roles.length === 0) return [];
  const seen = new Set<string>();
  const out: string[] = [];
  for (const r of roles) {
    if (!r) continue;
    if (r === "user") continue;
    if (r === "admin" && isAdmin) continue;
    if (seen.has(r)) continue;
    seen.add(r);
    out.push(r);
  }
  return out;
}

/**
 * Whether a given IANA zone string is one of the picker's curated
 * options. When the backend returns a zone outside this list (e.g. set
 * by the API directly), the `<select>` still renders the option as an
 * out-of-band entry so the user sees it; this helper signals to the
 * page whether to add that synthetic option.
 */
export function isCuratedTimeZone(zone: string | null | undefined): boolean {
  if (!zone) return false;
  return TIME_ZONE_OPTIONS.includes(zone);
}
