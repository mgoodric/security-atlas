# Slice 183 — UI honesty: calendar dead-link family + dashboard mockup refresh (build-time decisions)

> JUDGMENT-type build (slice doc front-matter is `Type: AFK` but two
> non-trivial shape decisions surfaced during build). Engineer made the
> calls in-flight per the `JUDGMENT` slice-development pattern in
> CLAUDE.md "AI-assist boundary": this governs HOW we build, NOT HOW
> the shipped product behaves. The product still never publishes
> audit-binding artifacts without one-click human approval — this
> slice is chrome.

---

## D1 — Tagged-union return for `linkFor` chosen over keeping a string return

**Decision:** Extract `linkFor` to a shared module at
`web/components/calendar/link-for.ts` and switch its return shape from
a bare `string` to a tagged union:

```ts
type LinkForResult =
  | { kind: "link"; href: string }
  | { kind: "static"; reason: string };
```

The agenda + month-grid views inspect `target.kind` and render a
`<Link>` (link case) or a non-linked `<span title={reason} aria-label={reason}>`
(static case).

**Why:**

- **Honesty.** The previous shape (string-returning) tempted both
  current and future authors into the "just return a path" trap. When
  the route doesn't exist, the natural answer was a placeholder href.
  The slice 178 audit categorized every such placeholder as a
  HONESTY-GAP. The tagged-union shape encodes the "no link" state as a
  distinct branch — TypeScript narrows it away on the link side, and
  the view code physically cannot render an anchor when the helper
  says `static`. The dead-anchor regression is structurally
  impossible without first widening the return type back to a string,
  which would be a deliberate, reviewable diff.
- **Exhaustiveness.** With the union return, the `default` branch
  reduces to `assertNever(ev.type)`. The `CalendarEvent["type"]`
  union is closed (`"audit" | "exception" | "policy" | "control"`), so
  the assertNever path is unreachable at compile time. Adding a fifth
  event type (e.g. `"vendor_assessment"` in a future slice) becomes
  a compile error at the helper site, not a silent dead anchor in the
  rendered UI. Slice 178 AC-5a's dead-anchor heuristic already catches
  the rendered HTML; this change catches it at type-check.
- **Single edit site.** Both `agenda-view.tsx` and
  `month-grid-view.tsx` previously duplicated the `linkFor` switch.
  When `/admin/exceptions/[id]` and `/policies/[id]` detail pages
  eventually ship, the flip from `static` to `link` is one diff in
  `link-for.ts` and both views update.

**Alternative rejected:** Keep the string return, change the
exception/policy branches to return `""` (empty string) and have the
view code conditionally render `<Link>` vs `<span>` based on the
truthiness of the string. Rejected because it (a) burns a magic
sentinel value (empty string), (b) requires duplicate
truthy-string-check logic at every render site, and (c) loses the
explanatory copy (the "reason" string in the static branch is the
tooltip text the user sees).

---

## D2 — `data-testid` deliberately omitted from the static-row spans

**Decision:** The non-linked `<span>` elements in
`agenda-view.tsx` and `month-grid-view.tsx` carry `title` and
`aria-label` for the disclosure tooltip but do NOT carry a
`data-testid`.

**Why:**

- **Manifest discipline.** Anti-criterion P0-183-3 forbids modifying
  `web/e2e-audit/mockup-spec.json`. The `/calendar` entry's
  `allowedExtraTestIds` list is empty (the calendar has `mockupPath:
null` and runs HONESTY-GAP heuristics only). Adding a new
  `data-testid` to the live page that the manifest doesn't list would
  trip the slice 178 audit harness's HONESTY-GAP detector — exactly
  the heuristic this slice is closing findings against.
- **Test reachability without testids.** The Playwright spec
  additions at `web/e2e/calendar.spec.ts` (AC-183-1 / AC-183-2) target
  the static rows via the existing `Exception` / `Policy` uppercase
  type label that the row already contains — no testid required. The
  vitest at `link-for.test.ts` covers the pure-logic disposition
  decision (link vs static) which is the load-bearing claim.
- **Future-readiness.** When `/admin/exceptions/[id]` and
  `/policies/[id]` detail pages ship, the `static` branch flips to a
  `link` branch and the spans become `<Link>`s. A leftover
  `data-testid` from this slice would be dead code at that point.

**Alternative considered:** Add testids and amend the manifest in the
same PR. Rejected because P0-183-3 is explicit ("Does NOT modify the
slice-178 audit harness or manifest"). The maintainer can add the
testids later as a separate manifest-update slice if testid-based
selectors prove preferable to text-based ones.

---

## D3 — Dashboard mockup body-anchors NOT touched (only sidebar entries)

**Decision:** `Plans/mockups/dashboard.html` has five `<a href="#">`
anchors inside the main body (lines 250, 398, 432, 445, 609 —
"View register →", "refresh now", "MFA enforcement · contractor
workspace", "Vuln remediation · critical → 7d", "View full activity
ledger →"). This slice does NOT remove or modify those anchors. Only
the two **sidebar** entries flagged by F-178-7 and F-178-8 (Vendors,
trailing `Admin`) are removed.

**Why:**

- **Scope match.** The slice doc's AC-4 names "the `Vendors` sidebar
  entry" and "the generic profile/help `#` entries" — those are the
  sidebar entries, full stop. F-178-7 and F-178-8 in the slice 178
  first-pass report use the same scope ("two `<a href=\"#\">` sidebar
  entries"). The body anchors were NOT flagged by slice 178.
- **Heuristic alignment.** The slice 178 dead-anchor heuristic runs
  against the LIVE page DOM, not the mockup HTML. The body
  `href="#"`s in `dashboard.html` are mockup-only — they never reach a
  live page. They function as design-doc placeholders for clickable
  card affordances that the live React `/dashboard` either ships
  differently or defers. Modifying them in this slice would be
  out-of-scope churn.
- **Spillover discipline.** If the maintainer judges the body anchors
  as a separate MOCKUP-STALE class (different from F-178-7/8), the
  correct path is a fresh spillover slice — not silently expanding
  this one. No such finding was filed by slice 178, so no spillover
  is filed by this slice either.

**Alternative rejected:** Sweep all five body anchors and convert them
to either real `*.html` mockup links or remove them entirely.
Rejected because (a) it widens scope beyond the spec's AC-4,
(b) deciding the correct rewrite for each anchor requires per-anchor
context (e.g. "View register →" arguably should link to `risks.html`
since the panel header says "Top risks · aging"; "refresh now"
arguably should be a button not a link), and (c) the mockups are
iteration-1 design-doc artifacts, not the source of truth for shipped
surfaces — overhauling them here would conflate two concerns.

---

## D4 — Helper lives under `components/calendar/`, not `app/(authed)/calendar/`

**Decision:** `link-for.ts` and `link-for.test.ts` are colocated with
the components that consume them at
`web/components/calendar/link-for.ts`. The vitest config grows two
narrow new globs (`components/**/*.test.ts` for tests,
`components/**/*.ts` for coverage) — both `.test.ts`-only, matching
the established node-env / no-JSX precedent.

**Why:**

- **Both consumers are sibling components.** Two views (`agenda-view`,
  `month-grid-view`) share the helper. Placing it under
  `app/(authed)/calendar/` would force a `../../../components/calendar`
  import path from a colocated route-level test, and would imply the
  helper is page-level (it isn't — it's a component-level utility).
- **Vitest narrow include.** The `components/**/*.test.ts` glob is
  intentionally `.test.ts`-only, never `.test.tsx`. The web workspace
  has zero `@testing-library/react` dependency by design (slice 069
  P0-A3); a `.tsx` test file would pull in JSX and fail in the
  node-env runner. The new glob preserves that boundary.
- **Coverage parity.** The coverage `include` mirrors the test
  `include` with a matching `components/**/*.tsx` exclude, so JSX
  view files never inflate or deflate the coverage report.

**Alternative considered:** Put the helper in `lib/calendar/` (sibling
to `lib/api.ts`). Rejected because `lib/` is the public-API /
shared-utilities surface; `link-for.ts` is component-area UI logic
specific to the calendar views — keeping it next to its callers is
the right blast radius.

---

## Revisit triggers

- When `/admin/exceptions/[id]` or `/policies/[id]` detail pages
  ship, flip the corresponding branch in
  `web/components/calendar/link-for.ts` from `{ kind: "static" }` to
  `{ kind: "link", href: ... }`. The two view components require no
  edit. Update the audit-log entry to note the closure.
- If a fifth `CalendarEventType` is added to the BFF, the helper's
  `default` branch will fail at compile time via `assertNever` — that's
  the intended behavior. Add the new case explicitly with the right
  disposition (link or static).
- If the maintainer decides body anchors in `Plans/mockups/dashboard.html`
  warrant cleanup, file a fresh spillover slice with parent reference
  to slice 183.
