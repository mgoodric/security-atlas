// #2086 — the entity picker's pure logic: the options each kind lists,
// and the check a pasted ID passes before the picker accepts it. #2087
// adds controls, risks and policies, and the multi-select's selection
// rule (`toggleSelection`).
//
// #2432 adds evidence, for the questionnaire's citation picker.
//
// Pure, so every case is vitest-testable without a DOM (Q-3). The
// `<EntityPicker>` and `<EntityMultiPicker>` components
// (`components/ui/entity-picker.tsx`) are the only render paths.

import type { TenantControl } from "./api/controls-list";
import type { EvidenceRecord, EvidenceSearchHit } from "./api/evidence";
import type { Policy } from "./api/policies";
import type { Risk } from "./api/risks";
import { enumLabel } from "./labels/enum-labels";
import { frameworkLabel } from "./labels/framework-label";
import { exactUTC } from "./relative-time";
import {
  apiKeyText,
  isMachineActor,
  normaliseUserId,
  resolvePerson,
  type DirectoryApiKey,
  type DirectoryUser,
} from "./person";

export type EntityKind =
  | "framework"
  | "user"
  | "credential"
  | "control"
  | "risk"
  | "policy"
  | "evidence";

export interface EntityOption {
  // The ID the form submits.
  value: string;
  // What the list and the input show. Never a raw ID for a listed entity.
  label: string;
  // Secondary text beside the label (a version's status, an owner).
  hint?: string;
  // Text the search also matches but the list does not show (a
  // control's bundle).
  keywords?: readonly string[];
  // The record kind, for a picker that lists more than one (the
  // citation picker cites a control or an evidence record).
  kind?: EntityKind;
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
  control: "control",
  risk: "risk",
  policy: "policy",
  evidence: "evidence record",
};

/** The plural noun for a kind, for the picker's own copy. */
export const KIND_PLURAL: Record<EntityKind, string> = {
  framework: "framework versions",
  user: "people",
  credential: "API keys",
  control: "controls",
  risk: "risks",
  policy: "policies",
  evidence: "evidence",
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
      label: apiKeyText(k.label, owner),
    };
  });
}

/**
 * #2414: machine and system actors (the Activity filter only), labelled
 * the way `<Person>` labels them ("System", "OAuth client"), with the
 * process or client ID as the hint so two of them stay apart. Anything
 * that is not a machine actor is dropped, and duplicates collapse.
 */
export function machineActorOptions(refs: readonly string[]): EntityOption[] {
  const out = new Map<string, EntityOption>();
  for (const raw of refs) {
    const a = raw.trim();
    if (!isMachineActor(a) || out.has(a)) continue;
    const colon = a.indexOf(":");
    out.set(a, {
      value: a,
      label: resolvePerson(a, null).text,
      hint: colon >= 0 ? a.slice(colon + 1) : undefined,
    });
  }
  return [...out.values()].sort(
    (x, y) =>
      x.label.localeCompare(y.label) ||
      (x.hint ?? "").localeCompare(y.hint ?? ""),
  );
}

/**
 * The tenant's controls, labelled by title. The SCF code and family are
 * the hint; the bundle is searchable too.
 */
export function controlOptions(
  controls: readonly TenantControl[],
): EntityOption[] {
  return controls.map((c) => ({
    value: c.id,
    label: c.title,
    kind: "control" as const,
    hint: [c.scf_id, c.control_family].filter(Boolean).join(" · ") || undefined,
    keywords: c.bundle_id ? [c.bundle_id] : undefined,
  }));
}

/** The tenant's risks, labelled by title, with the category as the hint. */
export function riskOptions(
  risks: readonly Pick<Risk, "id" | "title" | "category" | "category_name">[],
): EntityOption[] {
  return risks.map((r) => ({
    value: r.id,
    label: r.title,
    kind: "risk" as const,
    hint: r.category_name || r.category || undefined,
  }));
}

/** The tenant's policies, labelled by title, with version and status. */
export function policyOptions(
  policies: readonly Pick<Policy, "id" | "title" | "version" | "status">[],
): EntityOption[] {
  return policies.map((p) => ({
    value: p.id,
    label: p.title,
    kind: "policy" as const,
    hint:
      [
        p.version ? `v${p.version}` : "",
        p.status ? enumLabel("PolicyStatus", p.status) : "",
      ]
        .filter(Boolean)
        .join(" · ") || undefined,
  }));
}

/**
 * The tenant's evidence records. A record has no title, so the label is
 * its kind and the UTC day it was observed (2026-11-01, the same in every
 * locale, since the citation saves it as its title); the result is the
 * hint.
 */
export function evidenceOptions(
  records: readonly Pick<
    EvidenceRecord,
    "evidence_id" | "evidence_kind" | "observed_at" | "result"
  >[],
): EntityOption[] {
  return records.map((r) => {
    const observed = exactUTC(r.observed_at).slice(0, 10) || "undated";
    return {
      value: r.evidence_id.toLowerCase(),
      label: `${r.evidence_kind || "Evidence"} · ${observed}`,
      kind: "evidence" as const,
      hint: r.result ? enumLabel("EvidenceResult", r.result) : undefined,
    };
  });
}

/**
 * Evidence the server search matched for `query`. The search title
 * ("<kind> · <control_ref>") is the label. The search matched each hit
 * per token, server-side, so the query is a keyword: the client filter
 * keeps every hit the server returned for it.
 */
export function evidenceSearchOptions(
  hits: readonly EvidenceSearchHit[],
  query: string,
): EntityOption[] {
  return hits.map((h) => ({
    value: h.id.toLowerCase(),
    label: h.title,
    kind: "evidence" as const,
    keywords: [query.trim()],
  }));
}

/**
 * The evidence a picker offers: the suggested newest records, then what
 * the server search found for `searchFor`. Nothing at all once the
 * server has refused evidence: a query keeps its last data after a
 * failed refetch, and a title cached before the refusal must not show
 * beside "not available".
 */
export function evidencePickerOptions({
  refused,
  listed,
  found,
  searchFor,
}: {
  refused: boolean;
  listed: Parameters<typeof evidenceOptions>[0] | undefined;
  found: readonly EvidenceSearchHit[] | undefined;
  searchFor: string;
}): EntityOption[] {
  if (refused) return [];
  return [
    ...evidenceOptions(listed ?? []),
    ...evidenceSearchOptions(found ?? [], found ? searchFor : ""),
  ];
}

/**
 * Options from several sources, the first of each value kept, so a
 * record listed and also found by search appears once.
 */
export function uniqueOptions(
  options: readonly EntityOption[],
): EntityOption[] {
  const seen = new Set<string>();
  return options.filter((o) => {
    if (seen.has(o.value)) return false;
    seen.add(o.value);
    return true;
  });
}

/**
 * The selection after toggling `id`. An add past `max` is refused and
 * the selection comes back unchanged; a removal always applies.
 */
export function toggleSelection(
  selected: readonly string[],
  id: string,
  max?: number,
): string[] {
  if (selected.includes(id)) return selected.filter((x) => x !== id);
  if (max !== undefined && selected.length >= max) return [...selected];
  return [...selected, id];
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

/**
 * The error for text a list-only picker does not hold. The citation
 * picker is list-only: a pasted ID it cannot find has no kind to cite.
 */
export function notListedError(
  kinds: readonly EntityKind[],
  raw: string,
): string {
  const nouns = kinds.map((k) => KIND_NOUN[k]).join(" or ");
  return `"${raw.trim()}" is not in the list. Pick a ${nouns} from the list.`;
}

/** Case-insensitive match on the label or the hint. */
export function matchesQuery(option: EntityOption, query: string): boolean {
  const q = query.trim().toLowerCase();
  if (q === "") return true;
  return (
    option.label.toLowerCase().includes(q) ||
    (option.hint ?? "").toLowerCase().includes(q) ||
    (option.keywords ?? []).some((k) => k.toLowerCase().includes(q)) ||
    option.value === q
  );
}
