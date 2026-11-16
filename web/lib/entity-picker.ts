// #2086 — the entity picker's pure logic: the options each kind lists,
// and the check a pasted ID passes before the picker accepts it.
//
// Pure, so every case is vitest-testable without a DOM (Q-3). The
// `<EntityPicker>` component (`components/ui/entity-picker.tsx`) is the
// only render path.

import { enumLabel } from "./labels/enum-labels";
import { frameworkLabel } from "./labels/framework-label";
import {
  normaliseUserId,
  type DirectoryApiKey,
  type DirectoryUser,
} from "./person";

export type EntityKind = "framework" | "user" | "credential";

export interface EntityOption {
  // The ID the form submits.
  value: string;
  // What the list and the input show. Never a raw ID for a listed entity.
  label: string;
  // Secondary text beside the label (a version's status, an owner).
  hint?: string;
}

// The `GET /v1/frameworks?include=versions` wire shape.
export interface FrameworkWithVersions {
  id: string;
  name: string;
  versions?: { id: string; version: string; status: string }[];
}

const UUID_RE =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
const NIL_UUID = "00000000-0000-0000-0000-000000000000";

const KIND_NOUN: Record<EntityKind, string> = {
  framework: "framework version",
  user: "user",
  credential: "API key",
};

/** One option per framework version, labelled "<name> <version>". */
export function frameworkOptions(
  frameworks: readonly FrameworkWithVersions[],
): EntityOption[] {
  const out: EntityOption[] = [];
  for (const f of frameworks) {
    for (const v of f.versions ?? []) {
      out.push({
        value: v.id.toLowerCase(),
        label: frameworkLabel(f.name, v.version),
        hint: v.status
          ? enumLabel("FrameworkVersionStatus", v.status)
          : undefined,
      });
    }
  }
  return out.sort((a, b) => a.label.localeCompare(b.label));
}

/** Active users with a name, sorted by name. */
export function userOptions(users: readonly DirectoryUser[]): EntityOption[] {
  const out: EntityOption[] = [];
  for (const u of users) {
    const id = normaliseUserId(u.id);
    const name = u.display_name.trim();
    if (!id || !name || u.status === "disabled") continue;
    out.push({ value: id, label: name });
  }
  return out.sort((a, b) => a.label.localeCompare(b.label));
}

/** API keys, labelled by owner the way `<Person>` labels them. */
export function credentialOptions(
  apiKeys: readonly DirectoryApiKey[],
  users: readonly DirectoryUser[],
): EntityOption[] {
  const names = new Map<string, string>();
  for (const u of users) {
    const id = normaliseUserId(u.id);
    if (id) names.set(id, u.display_name.trim());
  }
  return apiKeys.map((k) => {
    const ownerId = k.owner_id ? normaliseUserId(k.owner_id) : null;
    const owner = ownerId ? names.get(ownerId) : undefined;
    return {
      value: k.id.trim().toLowerCase(),
      label: owner ? `API key (owner: ${owner})` : "API key",
    };
  });
}

export type PastedId = { ok: true; id: string } | { ok: false; error: string };

/**
 * Checks a pasted reference against the kinds a picker accepts. A user
 * ID may carry one `user:` prefix; every ID is a non-nil UUID, returned
 * lowercased.
 */
export function parsePastedId(
  kinds: readonly EntityKind[],
  raw: string,
): PastedId {
  const s = raw.trim();
  if (kinds.includes("user")) {
    const id = normaliseUserId(s);
    if (id) return { ok: true, id };
  }
  const lower = s.toLowerCase();
  if (
    kinds.some((k) => k !== "user") &&
    UUID_RE.test(lower) &&
    lower !== NIL_UUID
  ) {
    return { ok: true, id: lower };
  }
  const nouns = kinds.map((k) => KIND_NOUN[k]).join(" or ");
  return {
    ok: false,
    error: `"${s}" is not a valid ${nouns} ID. Pick one from the list or paste its ID.`,
  };
}

/** Case-insensitive match on the label or the hint. */
export function matchesQuery(option: EntityOption, query: string): boolean {
  const q = query.trim().toLowerCase();
  if (q === "") return true;
  return (
    option.label.toLowerCase().includes(q) ||
    (option.hint ?? "").toLowerCase().includes(q) ||
    option.value === q
  );
}
