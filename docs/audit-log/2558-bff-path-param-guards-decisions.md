# 2558 — Guard every BFF path param; harden the ratchet: decisions log

Issue: mgoodric/security-atlas#2558, a follow-up from the review of #2542
(#2312). Defence in depth: a `..` path param can re-target the upstream
`/v1` call to a parent path or verb, using the caller's own bearer under RLS.

## Detection-tier classification

- **detection_tier_actual:** unit. The hardened ratchet found four routes
  the #2312 ratchet had counted as guarded: two guarded after the bearer read
  (`controls/[id]/bundle`, `controls/imports/[id]/export`) and one GET with no
  guard (`policies/[id]`, whose PATCH and DELETE had it).
- **detection_tier_target:** unit.

## Decisions

### D1 — one helper for non-UUID params, allowlists per param shape

`rejectUnsafeSegment(segs, allow)` sits beside `rejectNonUUID` in
`web/lib/api/bff.ts`. It always refuses an empty, `.` or `..` segment, and any
segment holding `/`, `\` or `%`. Then it requires `allow`, which is either a
`SEGMENT` regex or the param's closed set. Each allowlist matches only what
the platform mints for that param:

| Allowlist              | Params                                                  | Source of the shape                                                              |
| ---------------------- | ------------------------------------------------------- | -------------------------------------------------------------------------------- |
| `SEGMENT.slug`         | `admin/features/[key]`, `metrics/[id]`                  | feature keys (`risk.themes`); metric catalog ids are snake_case (catalog loader) |
| `SEGMENT.slugOrUUID`   | `themes/[ref]`, `risks/categories/[ref]`                | `themeNamePattern` / `categorySlugPattern`; upstream takes "slug or id"          |
| `SEGMENT.token`        | `me/sessions/[id]`                                      | `sessions.NewID`, base64url                                                      |
| `SEGMENT.credentialID` | `admin/credentials/[id]`                                | `key_<uuid>` (apikeystore), or the bare UUID `parseCredID` also takes            |
| `SEGMENT.code`         | `anchors/[id]`, `requirements/[id]`                     | UUID or catalog code (`IAC-06.1`, `slug:version:code`) from imported frameworks  |
| closed set             | `board-packs/.../sections/[key]`, `csf/profiles/[kind]` | `board.SectionKeys`; `csfassessment.ValidKinds`                                  |

`SEGMENT.code` is the loosest: any printable text without a separator.
Framework codes come from imported catalogs (HIPAA's `164.308(a)(1)(ii)(A)`),
so a tighter character class would refuse real ones. It still refuses every
dot segment and separator, which is what the attack needs.

Every other param is a UUID upstream (`uuid.Parse` in the handler or a UUID
primary key), including evidence ids, question ids (`qid`), `runId`,
`proposalID`, `generationId`, `requirement_id` and `user_id`, so they use
`rejectNonUUID`. On multi-param routes every param is passed to a guard.

### D2 — closed sets the routes already checked stay as they are

`framework-scopes/[id]/[transition]`, `risks/categories/[ref]/[action]`,
`policies/[id]/[action]` and the other `[action]`/`[transition]` routes already
checked a closed set (`ALLOWED.has`, `.includes`, `Object.hasOwn`) and answered
404 on a miss. Their behaviour is kept. The framework-scopes check moved above
the bearer read. The ratchet accepts those three forms as a guard.

The CSF `[kind]` route's inline `current`/`target` check is replaced by the
closed-set helper, which now runs before the bearer read. It still answers
400; the message reads `kind must be one of current, target`.

### D3 — the ratchet has no exemption list

`web/app/api/bff-uuid-guard.test.ts` now:

- scans `route.ts`, `route.tsx` and `route.js`;
- strips block and line comments before matching;
- splits each file into exported handlers, and for each one that reads
  `params` requires every `[param]` in the path to be guarded, by key in a
  `rejectNonUUID` or `rejectUnsafeSegment` call or by a closed-set check;
- requires the guard to come before `cookies(`, any `/v1` path, `fetch(`, a
  `forward*(` call or a local `proxy(`/`upstreamPath(` helper;
- drives every handler of every dynamic route with `..` in each param in
  turn, other params valid, and checks for a 400 or 404 with no fetch and no
  cookie read.

`KNOWN_UNGUARDED` is gone, not kept empty: nothing remains that cannot be
guarded, so a new exemption should be a deliberate change to the test.

Self-tests pin the three new failure modes: a guard named only in a comment,
an unguarded second param, and a guard after the bearer read. Reverting one
guarded route (`admin/super-admins/[user_id]`) to `main` fails both the
static and the `..` checks.

### D4 — fixtures use UUIDs

Thirteen existing route tests passed ids like `"abc"`, `"q1"` or `"v1"`. They
now pass UUIDs, so they still exercise the forward path. The evidence BFF
test that asserted `../controls` was encoded upstream now asserts a 400 with
no fetch.

### D5 — the optional audit-period refetch is not in this PR

The ticket's optional item (invalidate the audit-period query after a freeze
or on focus, #2312 D3) is a UI change unrelated to path guards. It is left for
its own ticket.
