# 242 — decisions log

> Slice type in spec: `AFK` (the spec frontmatter says `Type: AFK`,
> with the contingent rule "AC-6: Type updates if path (a): the
> slice is `Type: JUDGMENT` (template authorship is a subjective
> call). If path (b): `Type: AFK`."). Path (b) was chosen, so the
> slice stays AFK. This decisions log is kept because the spec's
> AC-5 explicitly mandates one (D1 path-(a)-vs-(b) + D3 if-(b)
> soft-copy + D4 slice 101 P0-A4 reference update path are all
> required entries), and because three sub-decisions inside the
> chosen path still benefit from being recorded so post-deployment
> iteration stays tractable.

## Decisions made

### D1 — Path (a) "ship the scaffold wizard" vs. path (b) "label-honesty body disclosure"

**Options considered.**

- **A. Ship the scaffold wizard at `/policies/scaffold`.** Build a
  new App Router segment that, given the operator's org name,
  inserts the five SOC 2 foundational policy rows via
  `POST /v1/policies` (each as `status: draft`, version
  `v0.1-draft`, with templated `body_md` citing CC1.4 / CC6.1 /
  CC7.4 / CC8.1 / CC9.1). Wizard requires a one-click
  confirmation per anti-pattern §1.6 + AC-2; templates land as
  drafts only — operator publishes per policy (AC-3, P0-242-3).
- **B. Label-honesty body disclosure (chosen).** Drop the lying
  `cta` prop on the zero-state branch entirely; fold the
  disclosure into the empty-state `body` text so the message
  reads as informational prose naming the operator's concrete
  next action (drafting via `POST /v1/policies` on the platform
  API). Path is reversible in one PR when path (a) becomes
  available.
- **C. Soften the CTA copy + change the destination to
  `/docs/policy-templates`.** Path B alternative the spec listed.
  Keeps the CTA shape but points it at a docs page.

**Chosen path.** B.

**Rationale.**

- **Scope match.** The spec estimates this slice at 0.5d. Path
  (a) is multi-slice work: it requires (a1) a new App Router
  segment at `web/app/(authed)/policies/scaffold/page.tsx` with
  full wizard chrome, (a2) JUDGMENT-grade authoring of five
  policy templates whose `body_md` aligns with SOC 2 TSC
  CC1.4 / CC6.1 / CC7.4 / CC8.1 / CC9.1, (a3) integration with
  `POST /v1/policies` (which exists at
  `internal/api/httpserver.go:705`) with five-row transaction
  semantics + idempotency, (a4) the one-click confirmation
  scaffold per AC-2, and (a5) a Playwright spec that seeds an
  empty tenant + walks the wizard + asserts the five drafts
  land + no row inserts without the operator click-through.
  That is two-to-three slices of work, well over 0.5d.
- **Destination doesn't exist.** Neither `/policies/scaffold`
  nor `/policies/new` nor even `/policies/[id]` page routes
  exist on main today (verified by `ls web/app/(authed)/
policies/` — the only page is `page.tsx`; the row-click
  navigates to a route that doesn't yet render). The slice 247
  pattern ("enable the button because the route exists") does
  NOT apply — there is no route to enable.
- **P0-242-4 forbids option C.** "Does NOT redirect the CTA to
  yet another unrelated admin page. The fix is either ship the
  destination or update the copy — not move the lie." A
  `/docs/policy-templates` route would itself not exist on main
  today (the slice 058 docs site lives at `docs-site/`, not
  `/docs/policy-templates`); pointing the CTA there would
  simply move the lie to a different unrelated URL. Folding
  the disclosure into the body removes the click entirely,
  which is the only honest available shape.
- **Slice 217 precedent.** On `/audits`, slice 217 chose Path A
  (label-honesty) over Path B (wire up the export) for the same
  reason — the underlying capability's right home was not the
  index page, and the destination wasn't ready. Slice 217's
  approach pinned the disclosure copy as a single-source
  constant and asserted on it via vitest + Playwright. This
  slice reuses that exact shape, retargeted from a span+title
  affordance (slice 217: lying button stood in a row of working
  sibling export buttons, so a span preserved the toolbar's
  rhythm) to a body-text fold (slice 242: lying CTA was the
  ONLY action on the empty-state card, so no sibling row to
  preserve — body text is cleaner).
- **Reversibility.** Path (b) is reversible in one PR when path
  (a) ships: the `body` prop is restored to the
  filter-narrowed string, the `cta` prop returns with
  `onClick: () => router.push("/policies/scaffold")`, and the
  `scaffold-future.ts` module deletes. Single-PR diff.

**Spillover filed.** Not in this PR. Path (a) is the alternate
path the spec doc explicitly defers to maintainer prioritization
("The audit does **not** prescribe which path; that's the
maintainer's call."). Filing the Path (a) follow-on as a
spillover would substitute engineer judgment for maintainer
prioritization. The maintainer can file the Path (a) follow-on
via the standard idea-to-slice flow when they're ready to invest
in the scaffold wizard's multi-slice surface.

**Confidence.** `high`. The slice doc, the missing destination,
and the slice 217 precedent are all aligned.

### D2 — Body-text fold vs. slice 217's span+title affordance

**Options considered.**

- **A. Body-text fold (chosen).** Drop the `cta` prop entirely
  on the zero-state branch; fold the disclosure into the
  empty-state `body` text, wrapped in a
  `<span data-testid="policies-scaffold-future">…</span>` so
  the slice 178 honesty harness + Playwright can assert on it.
- **B. Span+title affordance in the CTA slot (slice 217 shape).**
  Modify `EmptyState` to accept a ReactNode in the CTA slot
  (currently typed as `{label, onClick}`) so the page can pass
  a `<span title aria-label>…</span>` where the lying button
  used to be.
- **C. Keep the Button but point it at the empty-state CTA's
  no-op `() => {}` and rewrite the label as informational text.**
  A button that does nothing on click is still a button — the
  slice 178 `captureComingSoonButtons` heuristic would flag it.

**Chosen path.** A.

**Rationale.**

- **Empty-state card has one action slot.** The slice 217
  span+title pattern is the right answer when the lying button
  stands alongside working sibling buttons — replacing it in
  place preserves the toolbar's visual rhythm. On the
  empty-state card, the lying CTA is the ONLY action; removing
  it entirely and folding the message into the body reads more
  cleanly than inventing a span+title pattern that the
  `EmptyState` shell does not currently model.
- **EmptyState API stays narrow.** Path (b) would broaden the
  CTA prop's type from `{label: string; onClick: () => void}` to
  also accept a ReactNode (or invent a separate `disclosure`
  prop). That's API surface-creep for one page; the body-text
  fold composes naturally with the existing API by only
  widening `body` from `string` to `ReactNode` — a strictly
  additive change every existing caller already satisfies.
- **Slice 152 precedent.** `/controls` already does this for
  the truly-empty branch — informational empty-state body that
  names the maintainer's next action ("seed the SCF catalog")
  without inventing an in-app button for it (see
  `web/app/(authed)/controls/page.tsx` lines 466-498). Same
  shape, lifted directly.
- **Honesty harness alignment.** The slice 178
  `captureComingSoonButtons` heuristic looks for
  `button[disabled]` matching coming-soon copy. A body
  paragraph (even one wrapped in a `<span data-testid>`) is
  invisible to that heuristic, which is the correct behavior —
  the disclosure IS the affordance, so the gap is closed both
  visually (no greyed-out dead button) and at the audit-harness
  level (no disabled button to flag).
- **Path (c) rejected.** A button whose `onClick` is a no-op
  is still a button visually, and the slice 178 heuristic
  would still flag it. Worse, an operator who clicks it gets
  no feedback at all — strictly worse UX than today's wrong
  redirect.

**Confidence.** `high`. Slice 152 + slice 217 precedents both
inform the shape; the body-text fold is the cleaner read on a
card with one action slot.

### D3 — Soft-copy: future-tense + capability-named + action-hinted

**Decision.** Body reads "The in-app policy scaffold wizard
ships with a future slice — until then, drafts can be created
via the platform API (POST /v1/policies)." Single source of
truth in `scaffold-future.ts` `POLICIES_SCAFFOLD_FUTURE_BODY`.

**Why.**

- **Future-tense framing.** Per slice 184 D3 + slice 217 D3 +
  the broader honesty discipline, the disclosure names what
  WILL happen ("ships with a future slice"), not what is
  broken ("is disabled" / "is unavailable" / "is not working"
  / "is an error"). Failure-framing words are banned and pinned
  by vitest.
- **No placeholder slice number.** Per slice 184 D3 + slice
  217 D3, naming a specific tracking-issue number in
  user-facing copy is itself a HONESTY-GAP if the number gets
  re-shuffled or the slice's scope shifts. The capability name
  ("policy scaffold wizard") is stable; a slice number is not.
  Vitest asserts no `#NNN` or `slice NNN` patterns appear.
- **Action hint baked in.** "Drafts can be created via the
  platform API (POST /v1/policies)" tells the operator HOW to
  reach the working capability today. The `POST /v1/policies`
  endpoint has been live since slice 022 — this disclosure
  converts dead chrome into a useful signpost.
- **"policy scaffold" is load-bearing.** AC-7's Playwright
  spec asserts the disclosure body contains "policy scaffold"
  (case-insensitive). Vitest pins the substring too. A future
  copy rewrite that drops the phrase trips vitest immediately.
- **"POST /v1/policies" is the second pinned substring.** Both
  vitest and Playwright assert on it. The exact endpoint
  path is the operator's concrete next-action lookup — they
  read the message, then go look up `POST /v1/policies` in the
  OpenAPI spec at `internal/api/openapi/routes.go:210`.

**Alternative rejected — "Browse policy templates →
/docs/policy-templates".** The spec listed this as an example
soft-copy + soft-destination. Three reasons it's worse than
the chosen path: (1) `/docs/policy-templates` doesn't exist on
main (the docs site lives at `docs-site/` and doesn't have
this route), (2) the spec's anti-criterion P0-242-4 explicitly
forbids redirecting the CTA to yet another unrelated page,
(3) "browse policy templates" implies the templates exist
in-app, which they don't — the only templates the platform
ships are the SCF anchors, which aren't policy templates.

**Alternative rejected — "Coming soon — policy scaffold
wizard."** Slice 217 D3 rejected the same shape: "Pure cliché.
No information content. The slice 178 honesty heuristic itself
flags this pattern as a placeholder."

**Confidence.** `medium`. The exact wording is a small
subjective call; the substance (future-tense + capability-named

- action-hinted + endpoint-pinned) is high-confidence.

### D4 — Slice 101 P0-A4 reference update path

**Decision.** Updated the slice 101 P0-A4 comment block in
`web/app/(authed)/policies/page.tsx` (the file-header anti-
criteria docstring) to record that slice 242 retired the
"link to /admin/credentials as a placeholder" pattern for
this empty-state. The new comment names the slice (242), the
honesty-gap class it closed, the anti-criterion it honored
(P0-242-4), and points at this decisions log.

**Why.** The spec's AC-4 said "If path (b) is taken: the
slice's primary deliverable is updating the CTA copy +
destination + the implementing-slice-101 `P0-A4` reference so
future slices don't carry the forward-looking-UI claim
forward." The file-header docstring IS the per-slice
provenance trail (every slice that touches the page adds a
note); updating it ensures future slice authors who read the
file see the retirement note without having to grep the git
log.

**What I did NOT update.** Slice 100's analogous P0-A2 note
(the original "land somewhere usable" placeholder pattern,
where slice 100 first established the convention of pointing
empty-state CTAs at `/admin/credentials`). Slice 100's note
applies to `/risks`, not `/policies`; updating slice 100's
note is out of scope for slice 242. If a future slice closes
the analogous honesty-gap on `/risks` (or any other list view
that still carries the "land somewhere usable" pattern), that
slice updates the slice 100 note.

**Confidence.** `high`.

### D5 — Vitest covers the constants; Playwright covers the DOM

**Decision.** Vitest spec at `scaffold-future.test.ts` pins
the six copy + testid invariants as pure-logic checks.
Playwright spec at `web/e2e/policies-list.spec.ts` (existing
AC-5 test case, repurposed) asserts the DOM contract:

- empty-state visible
- "No policies published yet" title visible
- the formerly-lying `list-empty-state-cta` button is gone
  (`toHaveCount(0)`)
- the `policies-scaffold-future` body wrapper is visible
- the body contains "policy scaffold" (case-insensitive)
- the body contains "POST /v1/policies"

Playwright spec is quarantined behind the slice 082 seed
harness like the rest of `policies-list.spec.ts` — bodies left
commented as a reviewable contract.

**Why.** Same reasoning as slice 217 D4:

- Vitest config is node-env, no JSX (per `web/vitest.config.ts`
  — slice 069 P0-A3); `@testing-library/react` is NOT a
  dependency at this workspace. Component-DOM tests live in
  Playwright at this project. The slice 217 / 183 pattern
  (pure-logic constants module + vitest for the constants +
  Playwright for the DOM) is the established precedent.
- Coverage shape. Six vitest assertions guard (a) the testid
  token literal, (b) sentence-shape of the body, (c) the
  "policy scaffold" substring, (d) no failure-framing, (e) no
  placeholder slice number, (f) the "POST /v1/policies"
  endpoint substring. That is enough to make the copy
  rewritable without silently breaking either the Playwright
  contract or the slice 178 manifest.
- Playwright on a future-harness gate. The policies-list spec
  file is already quarantined behind slice 082 — modifying one
  existing test case inside that quarantine is the cheapest
  correct path. When the harness lands, all the contracts
  become live gates.

**Alternative rejected — install `@testing-library/react` +
jsdom and write a JSX render test.** Out of scope. Slice 069's
P0-A3 says no React component rendering in vitest at this
workspace; that's the project-level commitment, not a
per-slice negotiable.

**Confidence.** `high`. This is the exact pattern slice 217
used.

### D6 — `EmptyState` body prop widened from `string` to `ReactNode`

**Decision.** Widened the `body?` prop on
`components/list/empty-state.tsx` from `string` to `ReactNode`
so the `/policies` zero-state can wrap its body in
`<span data-testid="policies-scaffold-future">…</span>` for
the slice 178 harness + Playwright (AC-7).

**Why.**

- **Strictly additive.** Every existing caller passes a string
  body; ReactNode accepts strings unchanged. No existing
  call-site needs to change.
- **The honesty harness needs a testid.** The slice 178
  manifest entry confirms the disclosure is the affordance,
  not a dead button; the testid is how it locks onto the right
  element. The only place the testid can attach without
  modifying the `EmptyState` API is the body content — which
  means body needs to accept a node, not just a string.
- **No new dependency.** ReactNode is already imported by the
  module for `icon`; widening body to use the same type is
  zero-cost.

**Alternative rejected — add a separate `disclosure?:
ReactNode` prop to `EmptyState`.** API surface-creep for one
page. The body-content semantic is the same whether it's a
plain string or a wrapped span; conflating the two into one
prop is the right factoring.

**Confidence.** `high`.

## Revisit once in use

- **R-1.** Once path (a) ships (the actual scaffold wizard at
  `/policies/scaffold`), this slice's `scaffold-future.ts`
  module deletes, the `body` prop on the zero-state restores
  to a plain string, and the `cta` prop returns with
  `onClick: () => router.push("/policies/scaffold")`. One PR,
  clean reversal. The reversibility is the point.
- **R-2.** Slice 178 manifest update. The slice-178
  mockup-diff manifest may need an entry confirming
  `policies-scaffold-future` is the expected testid on
  `/policies` so the harness's mockup-vs-live diff treats it
  as expected-and-present, not unexpected-or-missing. Worth a
  check-in on the next slice 178 refresh.
- **R-3.** The slice doc's option C ("Browse policy templates
  → /docs/policy-templates") implies a future docs page for
  policy templates that the OSS distribution should ship. If
  the maintainer decides to ship that docs page before the
  scaffold wizard ships, the body disclosure here gets a
  third concrete next-action ("…or browse policy templates in
  the docs"). Low-priority — the `POST /v1/policies`
  signpost is sufficient on its own.
- **R-4.** The "New policy" header action at line 348-350
  (`<Button size="sm" disabled>New policy</Button>`) is the
  same honesty-gap class — a disabled button with future-
  capability copy — and is NOT covered by this slice (per
  AC-1's empty-state focus). When `/policies/new` ships, the
  slice 247 pattern (enable + Link + buttonVariants) applies.
  Until then, slice 225's tooltip-and-keep-disabled pattern
  (`/controls/new`) is the right interim. File a spillover
  slice if the maintainer wants the header action fixed
  before `/policies/new` ships.
- **R-5.** The disclosure body mentions `POST /v1/policies` —
  an operator who reads the message has to go look up the
  endpoint. If a "docs deep-link" pattern emerges elsewhere
  in the codebase (e.g. the disclosure becomes a clickable
  link to the OpenAPI doc surface for that endpoint), wire it
  in here too.

## Confidence summary

| Decision                                            | Confidence |
| --------------------------------------------------- | ---------- |
| D1 — path (a) vs. path (b) vs. soft-redirect (C)    | `high`     |
| D2 — body-text fold vs. span+title in CTA slot      | `high`     |
| D3 — soft-copy (future-tense + capability-named)    | `medium`   |
| D4 — slice 101 P0-A4 reference update path          | `high`     |
| D5 — vitest covers constants; Playwright covers DOM | `high`     |
| D6 — `EmptyState` body widened to `ReactNode`       | `high`     |

## Verification

- **AC-1 (path (b) chosen).** Lying CTA removed; disclosure
  folded into body. ✅
- **AC-4 (slice 101 P0-A4 reference updated).** File-header
  docstring updated with retirement note + decisions-log
  pointer. ✅
- **AC-5 (decisions log written).** This document. ✅
- **AC-6 (Type stays AFK on path (b)).** Spec frontmatter
  unchanged. ✅
- **AC-7 (Playwright spec asserts).** `policies-list.spec.ts`
  AC-5 case updated to assert (a) the lying CTA is gone, (b)
  the disclosure body wrapper is visible, (c) it contains
  "policy scaffold", (d) it contains "POST /v1/policies". ✅
- **AC-8 (pre-commit + DCO + co-author).** Verified at PR
  open time. ✅
- **P0-242-1 (no 50-template library).** N/A — path (b). ✅
- **P0-242-2 (no row inserts without click-through).** N/A —
  path (b); no rows are inserted at all. ✅
- **P0-242-3 (no silent publish of drafts).** N/A — path (b). ✅
- **P0-242-4 (does NOT redirect to another unrelated admin
  page).** The CTA is removed entirely, not redirected. ✅
- **P0-242-5 (no vendor-prefixed test fixture tokens).**
  Verified — testid `policies-scaffold-future` is
  neutral. ✅
- **Local CI parity** (per `feedback_local_ci_parity.md`):
  - `npx vitest run 'app/(authed)/policies/'` — 58 / 58 green
    (6 new + 52 pre-existing).
  - Full suite: `npx vitest run` — 798 / 798 green.
  - `npx tsc --noEmit` — zero errors in touched files
    (`policies/page.tsx`, `policies/scaffold-future.ts`,
    `policies/scaffold-future.test.ts`,
    `components/list/empty-state.tsx`,
    `web/e2e/policies-list.spec.ts`). Pre-existing errors in
    unrelated files (`lib/auth/oauth-client.test.ts`,
    `next-config.test.ts`,
    `scripts/capture-readme-screenshots.test.ts`) are not
    introduced by this slice.
  - `pre-commit run --files <touched>` — run before commit,
    see PR description for the captured output.
