// #2115 — the framework-scope wizard and the scope page's header action,
// as pure functions so the node vitest tier covers them (slice 069 P0-A3).
// `/framework-scopes/new` and `/framework-scopes/<version>` render them.
//
// The one security-relevant rule: the scope picker emits null while
// "choose by dimension" has no value picked, and the platform reads a
// stored null predicate as every cell (internal/scope isEmptyExpr). A null
// predicate, from the picker or typed as `null` under Advanced, is an
// incomplete form: `validateScopeDraft` builds no body for it, so nothing
// is sent. Everything is the explicit `{"op":"true"}`.
//
// Advanced JSON is only parsed here (JSON.parse), never evaluated; the
// platform validates the predicate's grammar on create.

import type {
  FrameworkScope,
  FrameworkScopeState,
} from "@/lib/api/framework-scopes";

export type ScopeDraft = {
  frameworkVersionId: string;
  name: string;
  /** The picker's emission, or Advanced's parsed JSON; null while incomplete. */
  predicate: unknown;
};

export type ScopeDraftErrors = Partial<
  Record<"frameworkVersion" | "name" | "scope", string>
>;

export type ScopeCreateBody = {
  framework_version_id: string;
  name: string;
  predicate: object;
};

export type ScopeDraftResult =
  | { ok: true; body: ScopeCreateBody }
  | { ok: false; errors: ScopeDraftErrors };

const SCOPE_INCOMPLETE = "Pick at least one value, or choose Everything.";

function isPredicateObject(p: unknown): p is object {
  return typeof p === "object" && p !== null && !Array.isArray(p);
}

/**
 * Checks a draft and builds the create body. No body is built while any
 * field is wrong, and never for a null predicate.
 */
export function validateScopeDraft(draft: ScopeDraft): ScopeDraftResult {
  const errors: ScopeDraftErrors = {};
  if (!draft.frameworkVersionId) {
    errors.frameworkVersion = "Choose the framework version to scope.";
  }
  const name = draft.name.trim();
  if (!name) errors.name = "Name the scope.";
  if (!isPredicateObject(draft.predicate)) errors.scope = SCOPE_INCOMPLETE;
  if (Object.keys(errors).length > 0) return { ok: false, errors };
  return {
    ok: true,
    body: {
      framework_version_id: draft.frameworkVersionId,
      name,
      predicate: draft.predicate as object,
    },
  };
}

export type ParsedAdvanced =
  | { ok: true; predicate: object }
  | { ok: false; error: string };

/** Parses the Advanced editor's text into a predicate object. */
export function parseAdvanced(text: string): ParsedAdvanced {
  let parsed: unknown;
  try {
    parsed = JSON.parse(text);
  } catch (e) {
    return { ok: false, error: "Invalid JSON: " + (e as Error).message };
  }
  if (!isPredicateObject(parsed)) {
    return {
      ok: false,
      error:
        'A scope predicate is a JSON object, such as {"op":"true"} for everything.',
    };
  }
  return { ok: true, predicate: parsed };
}

/** The Advanced editor's starting text for a predicate (null shows Everything). */
export function advancedText(predicate: unknown): string {
  return JSON.stringify(predicate ?? { op: "true" }, null, 2);
}

export function isFrozen(state: FrameworkScopeState): boolean {
  return state === "activated" || state === "superseded";
}

/**
 * The revision the page header acts on: the newest one still in the
 * workflow (draft, review or approved), else none.
 */
export function workingRevision(
  scopes: readonly FrameworkScope[],
): FrameworkScope | undefined {
  return [...scopes]
    .filter((s) => !isFrozen(s.state))
    .sort((a, b) => b.created_at.localeCompare(a.created_at))[0];
}

/** The revision a change starts from: the activated one, else the newest superseded. */
export function revisionSource(
  scopes: readonly FrameworkScope[],
): FrameworkScope | undefined {
  const active = scopes.find((s) => s.state === "activated");
  if (active) return active;
  return [...scopes]
    .filter((s) => s.state === "superseded")
    .sort((a, b) => b.created_at.localeCompare(a.created_at))[0];
}

export type HeaderAction =
  | { kind: "create" }
  | { kind: "submit"; scope: FrameworkScope }
  | { kind: "approve"; scope: FrameworkScope }
  | { kind: "activate"; scope: FrameworkScope }
  | { kind: "revise"; scope: FrameworkScope };

/** The one lifecycle action the scope page's header offers. */
export function headerAction(scopes: readonly FrameworkScope[]): HeaderAction {
  const w = workingRevision(scopes);
  if (w?.state === "draft") return { kind: "submit", scope: w };
  if (w?.state === "review") return { kind: "approve", scope: w };
  if (w?.state === "approved") return { kind: "activate", scope: w };
  const src = revisionSource(scopes);
  if (src) return { kind: "revise", scope: src };
  return { kind: "create" };
}

/** The URL of the wizard, pre-filled with a framework version. */
export function newScopeHref(frameworkVersionId?: string): string {
  return frameworkVersionId
    ? `/framework-scopes/new?framework_version_id=${encodeURIComponent(
        frameworkVersionId,
      )}`
    : "/framework-scopes/new";
}

/** The platform's `{"error": "..."}` message, else the status line. */
export async function errorMessage(res: Response): Promise<string> {
  try {
    const j = (await res.json()) as { error?: string };
    if (j.error) return j.error;
  } catch {
    // body not JSON — keep the status line
  }
  return `${res.status} ${res.statusText}`;
}
