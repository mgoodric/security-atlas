// #2085 — resolves an actor reference (a user ID, a `user:`-prefixed
// subject, or a credential ID) to the text a page shows in its place.
//
// Pure, so every case is vitest-testable without a DOM (Q-3). The
// `<Person>` component (`components/ui/person.tsx`) is the only render
// path; it feeds this function the tenant's directory.
//
// The visible text is never the raw ID. An unresolvable reference reads
// "Unknown" and carries the ID in the tooltip only.

import { deriveInitials } from "./display-name";

export interface DirectoryUser {
  id: string;
  display_name: string;
  // "active" | "disabled" (users.status CHECK).
  status: string;
}

export interface DirectoryApiKey {
  id: string;
  // users(id) of the key's owner (api_keys.issued_by); absent when the
  // key has none. The minting admin is never a stand-in.
  owner_id?: string;
  // The admin-chosen name for the key (api_keys.label, #2384); absent
  // when the key has none.
  label?: string;
}

// The tenant's people, keyed by normalised ID.
export interface PersonDirectory {
  users: Map<string, DirectoryUser>;
  apiKeys: Map<string, DirectoryApiKey>;
}

type PrincipalKind = "user" | "api_key" | "former_user";
export type PersonKind = PrincipalKind | "system" | "oauth_client" | "unknown";

export interface ResolvedPerson {
  kind: PersonKind;
  // Visible text. Never a raw ID.
  text: string;
  // Avatar initials; set for kind "user" only.
  initials?: string;
  // Tooltip.
  title: string;
}

const UUID_RE =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
const NIL_UUID = "00000000-0000-0000-0000-000000000000";

/**
 * Normalises a user reference the way `identity.UserID` does on the
 * backend: strip one `user:` prefix, require a non-nil UUID. Returns the
 * lowercased UUID, or null when the reference is not a user ID.
 */
export function normaliseUserId(ref: string): string | null {
  const s = ref
    .trim()
    .replace(/^user:/, "")
    .toLowerCase();
  if (!UUID_RE.test(s) || s === NIL_UUID) return null;
  return s;
}

/** Builds a directory from the people endpoint's lists. */
export function buildDirectory(
  users: readonly DirectoryUser[],
  apiKeys: readonly DirectoryApiKey[],
): PersonDirectory {
  const dir: PersonDirectory = { users: new Map(), apiKeys: new Map() };
  for (const u of users) {
    const id = normaliseUserId(u.id);
    if (id) dir.users.set(id, u);
  }
  for (const k of apiKeys) {
    dir.apiKeys.set(k.id.trim().toLowerCase(), k);
  }
  return dir;
}

function userName(u: DirectoryUser): string {
  return u.display_name.trim();
}

const UNKNOWN_TEXT = "Unknown";

/**
 * #2384 — the text an API key reads as: "API key '<label>' (owner:
 * <name>)", dropping the label or the owner when the key has none. Built
 * from the label and owner name only; the token, prefix and last four are
 * never inputs.
 */
export function apiKeyText(label?: string, ownerName?: string): string {
  const name = label?.trim();
  const base = name ? `API key '${name}'` : "API key";
  return ownerName ? `${base} (owner: ${ownerName})` : base;
}

// The principal a machine token records (`identity.UserID` on the
// backend names it as "oauth_client:<id>").
const OAUTH_CLIENT_PREFIX = "oauth_client:";

/**
 * Whether an actor reference names a machine or system actor rather than
 * a person or API key: "system", "system:<process>" or
 * "oauth_client:<id>". The shapes GET /v1/activity/actors lists.
 */
export function isMachineActor(ref: string): boolean {
  const s = ref.trim();
  return (
    s === "system" ||
    s.startsWith("system:") ||
    (s.startsWith(OAUTH_CLIENT_PREFIX) && s.length > OAUTH_CLIENT_PREFIX.length)
  );
}

const ANY_UUID_RE =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

/**
 * #2385 — whether a value is an actor reference rather than free text:
 * a UUID (a user or an API key), a `user:` or `jwt:` subject, a
 * credential ID (`key_…`, `bootstrap-admin`, credstore.PrincipalOf), or
 * a machine actor. A field that holds either a typed name or a
 * reference (a board pack's `published_by`, an evidence record's
 * `actor_id`) resolves the reference and shows anything else as written.
 */
export function isActorReference(ref: string): boolean {
  const s = ref.trim();
  return (
    ANY_UUID_RE.test(s) ||
    s.startsWith("user:") ||
    s.startsWith("jwt:") ||
    s.startsWith("key_") ||
    s === "bootstrap-admin" ||
    isMachineActor(s)
  );
}

/**
 * #2385 — the visible text for a field that holds either a typed name or
 * an actor reference, where only a string fits (a filter option's
 * label): the name as written, or the reference resolved.
 */
export function actorText(
  ref: string,
  dir: PersonDirectory | null | undefined,
): string {
  return isActorReference(ref) ? resolvePerson(ref, dir).text : ref;
}

function unknown(ref: string): ResolvedPerson {
  return {
    kind: "unknown",
    text: UNKNOWN_TEXT,
    title: ref.trim() ? `Unknown (${ref.trim()})` : UNKNOWN_TEXT,
  };
}

/**
 * Resolves an actor reference against the directory.
 *
 * - an active user → their name and initials;
 * - a disabled user → "Former user";
 * - an API key → "API key '<label>' (owner: <name>)" (apiKeyText), the
 *   label left out when the key has none and the owner when it does not
 *   resolve. The token, prefix and last four characters are never shown;
 * - "system" or "system:<process>" → "System", as the platform's
 *   people.Directory.Name reads it (#2138);
 * - "oauth_client:<id>" (a machine token) → "OAuth client", the client
 *   ID in the tooltip (#2414);
 * - anything else (a JWT credential, another tenant's user, a missing
 *   directory) → "Unknown", the reference in the tooltip.
 */
export function resolvePerson(
  ref: string | null | undefined,
  dir: PersonDirectory | null | undefined,
): ResolvedPerson {
  const raw = (ref ?? "").trim();
  if (raw === "system" || raw.startsWith("system:")) {
    return { kind: "system", text: "System", title: raw };
  }
  if (raw.startsWith(OAUTH_CLIENT_PREFIX)) {
    return { kind: "oauth_client", text: "OAuth client", title: raw };
  }
  if (!raw || !dir) return unknown(raw);

  const userId = normaliseUserId(raw);
  if (userId) {
    const u = dir.users.get(userId);
    if (u) {
      const name = userName(u);
      if (u.status === "disabled") {
        return {
          kind: "former_user",
          text: "Former user",
          title: name ? `Former user: ${name}` : "Former user",
        };
      }
      if (name) {
        return {
          kind: "user",
          text: name,
          initials: deriveInitials(name),
          title: name,
        };
      }
    }
  }

  // A `user:` subject never names a key.
  if (!raw.startsWith("user:")) {
    const key = dir.apiKeys.get(raw.toLowerCase());
    if (key) {
      const ownerId = key.owner_id ? normaliseUserId(key.owner_id) : null;
      const owner = ownerId ? dir.users.get(ownerId) : undefined;
      const text = apiKeyText(key.label, owner ? userName(owner) : "");
      return { kind: "api_key", text, title: text };
    }
  }

  return unknown(raw);
}
