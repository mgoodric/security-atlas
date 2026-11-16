// #2088 — the API-key scope builder's model.
//
// A key's scope is two things: the evidence kinds it may push
// (`allowed_kinds`) and a scope predicate over scope dimensions. The
// predicate uses the `internal/scope` grammar the scope-dimension picker
// (#2103) emits, so a key, an exception and a framework scope share one
// predicate language. Controls and frameworks are not part of a key's
// scope: nothing on the push path enforces them (see #2435).
//
// Pure, so it is covered in the node vitest tier; `page.tsx` renders it.

import { parseAdvanced } from "@/app/(authed)/framework-scopes/wizard";
import {
  EVERYTHING,
  fromPredicate,
} from "@/components/scope/scope-picker-model";

/**
 * The scope_predicate string to send. Everything goes as `{}`, the
 * column default, which reads as unrestricted.
 */
export function predicateBody(predicate: unknown): string {
  const loaded = fromPredicate(predicate);
  if (loaded.kind === "picker" && loaded.state.mode === "everything") {
    return "{}";
  }
  return JSON.stringify(predicate);
}

type RawOk = { ok: true; predicate: object };
type RawRefused = { ok: false; error: string };
export type RawToPicker = RawOk | RawRefused;

/**
 * Parses the raw JSON editor's text as written: blank is Everything, and
 * anything else must be a JSON object. Used to issue from raw JSON, so a
 * predicate the builder cannot show still goes through.
 */
export function parseRaw(text: string): RawToPicker {
  if (text.trim() === "") return { ok: true, predicate: EVERYTHING };
  return parseAdvanced(text);
}

/**
 * Parses the raw JSON editor's text for a switch back to the builder.
 * Blank text is Everything. A predicate the builder cannot show (any-of,
 * negation, a custom dimension, …) is refused with the reason, and the
 * editor stays open: the key can still be issued with it as written.
 */
export function rawToPicker(text: string): RawToPicker {
  const parsed = parseRaw(text);
  if (!parsed.ok) return { ok: false, error: parsed.error };
  const loaded = fromPredicate(parsed.predicate);
  if (loaded.kind === "advanced") {
    return {
      ok: false,
      error: `The builder can't show this predicate: it ${loaded.reason}. Keep editing it here, or issue the key with it as written.`,
    };
  }
  return { ok: true, predicate: parsed.predicate };
}

/**
 * The evidence kinds offered: each registered kind once, sorted, plus
 * any kind already selected (one added by hand) so it never disappears.
 */
export function kindOptions(
  schemas: readonly { kind: string }[],
  selected: readonly string[],
): string[] {
  const out = [...new Set(schemas.map((s) => s.kind))].sort();
  for (const k of selected) if (!out.includes(k)) out.push(k);
  return out;
}

/** Toggles one kind, keeping the selection in option order. */
export function toggleKind(
  selected: readonly string[],
  kind: string,
  options: readonly string[],
): string[] {
  const next = selected.includes(kind)
    ? selected.filter((k) => k !== kind)
    : [...selected, kind];
  const rank = (k: string) => {
    const i = options.indexOf(k);
    return i === -1 ? options.length : i;
  };
  return next.sort((a, b) => rank(a) - rank(b));
}
