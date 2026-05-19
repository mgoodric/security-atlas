// Slice 166 — null-safe rendering of api_keys.allowed_kinds.
//
// The Postgres column `api_keys.allowed_kinds` is `TEXT[] NOT NULL DEFAULT
// '{}'::text[]` so a literal NULL is impossible at the schema level. However
// pgx-go decodes an empty `'{}'::text[]` value to a Go nil `[]string`, and Go's
// `encoding/json` marshals a nil slice as `null` (not `[]`). So
// `GET /api/admin/credentials` legitimately returns
// `"allowed_kinds": null` for any row whose stored array is empty — which is
// the default state of every newly-issued admin credential.
//
// The TypeScript type declares `allowed_kinds: string[]` (slice doc P0-A4:
// the wire shape semantics are unchanged — null and `[]` both mean "any
// kind"; null is a bug-shaped artifact of the encoder chain, not a separate
// semantic). Rather than propagate the lie into the type, we coerce on
// READ at the single render site.
//
// `kindsLabel` returns either "any" (the empty / nil-equivalent case) or the
// comma-joined list. Callers render the literal string "any" with a muted
// style; everything else renders as a plain join. The two-branch render
// would otherwise have to null-guard twice (length deref + join deref), so
// collapsing it into one pure helper removes both crash sites at once.

/** Sentinel returned for the empty-or-nil case. The credentials table renders
 *  this string with a muted style to indicate "no restriction" — the same
 *  semantics the backend's nil-or-empty array carries.
 */
export const ALLOWED_KINDS_ANY = "any";

/** Pure renderer: returns "any" for null/undefined/empty input; otherwise
 *  the comma-joined list. No throw, no exception path, no side effects.
 *  The caller decides the styling — this helper only decides the text.
 */
export function kindsLabel(kinds: string[] | null | undefined): string {
  if (!kinds || kinds.length === 0) {
    return ALLOWED_KINDS_ANY;
  }
  return kinds.join(", ");
}

/** Returns true when the helper should render the "any" sentinel — useful for
 *  callers that want to apply a different styling to the sentinel case (e.g.,
 *  a muted span) vs the joined-list case.
 */
export function isAnyKind(kinds: string[] | null | undefined): boolean {
  return !kinds || kinds.length === 0;
}
