# 233 — UI honesty: /evidence "Push evidence" CTA affordance

**Slice:** `docs/issues/233-ui-honesty-evidence-push-button-disabled.md`
**Type:** AFK (decisions log captured at orchestrator request — not a
JUDGMENT slice; the spec defaulted to Option A and the maintainer-equivalent
calls below are within the choice envelope the spec already framed)
**Branch:** `frontend/233-evidence-push-cta`
**Parent:** slice 204 (UI parity audit fleet, /evidence page audit)

## Decisions made

### D1 — Pick Option A (link to canonical doc) over Option B (in-app push UI)

- **Chosen.** Option A — replace the permanently-disabled `<Button>` with
  a primary-styled `<a>` linking to the canonical CLI push documentation.
  Add a second sentence to the page subtitle directing operators to the
  same destination via an inline link.
- **Considered.** Option B — ship an in-app `<Dialog>` with a JSONL paste
  textarea + manual evidence upload form that POSTs to `/v1/evidence:push`
  via the BFF. ~2.0d with wire reuse from slice 003 end-to-end.
- **Rationale.** The slice 233 spec defaults to Option A. Option B is the
  shape if/when a maintainer wants in-app push as a first-class surface
  worth investing UI bandwidth in; until then, signposting the existing
  CLI/SDK pathway is the honest minimum. The user prompt for this slice
  explicitly forbids Option B ("NO building the in-app push UI (that's
  slice 003 frontend follow-on)") so the choice is forced.
- **Confidence.** high. Spec defaulted to A; the prompt re-pinned A as a
  hard anti-criterion.

### D2 — Destination = `/docs/primitives/evidence#pushing-evidence-from-your-own-tools`, not `/admin/credentials`

- **Chosen.** The link's `href` is the deep-link anchor `#pushing-evidence-
from-your-own-tools` on the evidence-primitive doc page. That section
  carries the canonical `just atlas-cli evidence push` example and the
  push-credential / authentication boilerplate.
- **Considered.** `/admin/credentials` — the slice 233 spec's primary
  alternative. Rejected because no `/admin/credentials` page exists in
  `web/app/admin/` (only `web/app/admin/api-keys/page.tsx` does). The
  existing slice 099 empty-state CTA at `evidence/page.tsx` line 373 ALSO
  navigates to `/admin/credentials` and is itself broken — that is its
  own honesty gap which a future slice should resolve. Linking a brand-new
  surface to a broken route would compound the gap; linking to the docs
  primitive is a real, reachable destination.
- **Considered.** `/docs/cli` — the spec's secondary alternative. Rejected
  because no top-level `cli.md` exists under `docs-site/docs/` (the
  CLI quickstart content lives inside the primitive docs and the install
  walkthrough — there is no single "CLI quickstart" page to anchor).
  The evidence-primitive deep-link is the canonical surface for the
  push example, so anchor there directly.
- **Rationale.** "Honesty > parity" — link to a destination that
  actually exists and actually carries the CLI push example the CTA
  promises.
- **Confidence.** high. The docs route was verified by grepping
  `docs-site/docs/primitives/evidence.md` for the `## Pushing evidence
from your own tools` heading; the `atlas-cli evidence push` example
  is at lines 97-128 of that file.

### D3 — Open the link in a new tab (`target="_blank"` + `rel="noreferrer"`)

- **Chosen.** Both the action-bar CTA and the inline subtitle link open
  in a new tab via `target="_blank"` and `rel="noreferrer"`.
- **Considered.** Same-tab navigation (no `target` attribute). Rejected
  because the operator landing on `/evidence` is mid-investigation —
  filters set, ledger window open. Pulling them to a docs page means
  the back button is the only path home, and any filter pills they had
  configured are gone if Next does a fresh route resolve.
- **Rationale.** External-style links (docs pages) open in new tabs
  by convention across this codebase. The existing in-tree Export
  `<a>` links (line 328-336) trigger browser file-save dialogs, not
  navigation — different intent, different shape.
- **Confidence.** high. Standard UX convention; no in-house anti-precedent.

### D4 — Extract `push-cta.ts` for unit-testability

- **Chosen.** Create `web/app/(authed)/evidence/push-cta.ts` exporting
  `PUSH_CTA_LABEL`, `PUSH_CTA_HREF`, `PUSH_CTA_SUBTITLE_PREFIX`, and a
  `pushCtaSubtitleSuffix()` helper. Cover with
  `push-cta.test.ts` — five `vitest` cases pinning label, href,
  prefix, no-disabled-affordance shape, and the concatenated suffix.
- **Considered.** Inline literals in `page.tsx`. Cheapest in lines of
  code but leaves no testable surface — `web/vitest.config.ts` is
  node-env / no-JSX (slice 069 P0-A3), so the JSX itself cannot be
  rendered to assert "the CTA is no longer disabled".
- **Considered.** Add `@testing-library/react` for component-level
  assertions. Rejected for the same reason slice 219 / 222 / 248
  rejected it: re-opening the slice 069 vitest-config carve-out for a
  small honesty-gap fix is a precedent-shift that would inflate the
  PR beyond its scope.
- **Considered.** Add a Playwright spec assertion that runs live. Rejected
  as PRIMARY guard because the existing `web/e2e/evidence-list.spec.ts`
  assertions are all commented-out (quarantined behind the slice 082
  seed harness, per the slice 079 decision). Adding a commented
  assertion documents the contract but it does not run; the unit
  test is the live regression gate.
- **Rationale.** Mirrors slice 219 (pack-header-meta.ts +
  pack-header-meta.test.ts) and slice 222 (posture-coverage-
  caption.ts + .test.ts) — the established pattern in this module is
  "extract pure logic into `.ts`, unit-test the constant, leave the
  JSX wiring trivial".
- **Confidence.** high. Three-slice precedent run.

### D5 — Add a commented Playwright assertion for the live render contract

- **Chosen.** Append a new test block at the bottom of
  `web/e2e/evidence-list.spec.ts` whose body is entirely commented
  out, mirroring the existing seven test blocks in the same file.
  The assertions document: CTA is visible, enabled, has the right
  `href`, has `target="_blank"`, and the inline subtitle link
  resolves to the same destination.
- **Considered.** Un-comment the assertion and ship a live e2e gate.
  Rejected because the slice 082 seed harness is the established gate
  for un-commenting any `evidence-list.spec.ts` assertion; this slice
  is not in scope for that work, and a single un-commented assertion
  would diverge from the rest of the file's quarantined contract.
- **Considered.** Skip the Playwright update entirely. Rejected because
  AC-3 of the slice 233 spec explicitly calls for the spec to gain a
  new assertion confirming the link's `href` is navigable. The
  commented-spec convention is how this file already documents
  contracts pre-seed-harness.
- **Confidence.** high. Established pattern.

### D6 — Subtitle reuses the same `data-testid="evidence-push-cta-inline"` shape

- **Chosen.** The inline link inside the subtitle node carries
  `data-testid="evidence-push-cta-inline"` (note the trailing
  `-inline`), distinct from the action-bar CTA's
  `data-testid="evidence-push-cta"`. Both `href`s resolve to the same
  destination.
- **Considered.** Use a single shared testid on both surfaces.
  Rejected — Playwright selectors must disambiguate the two render
  sites (one in the page header, one in the action-bar row); a
  shared testid would surface a multi-match warning.
- **Considered.** Drop the inline subtitle link entirely; carry only
  the action-bar CTA. Rejected — AC-2 of the slice 233 spec
  explicitly requires the subtitle to gain a follow-up sentence
  with the second clause linked.
- **Confidence.** high.

### D7 — Diverge from the mockup (`Plans/mockups/evidence.html`)

- **Chosen.** Do NOT update `Plans/mockups/evidence.html` in this
  slice. The mockup still renders the `bg-brand-600` styled "Push
  evidence" button as a live `<button>` (lines 117-121).
- **Considered.** Bundle a mockup edit aligning to Option A. Rejected
  — mockup edits are a separate slice type (slice 220 precedent).
  Mixing chrome scope here would inflate the PR.
- **Confidence.** medium. The next mockup-alignment slice (whoever
  files it) decides whether to update the mockup to match Option A
  or to render a richer in-page push affordance (Option B).

### D8 — Anti-criteria scan: confirm none of the P0s are at risk

- **Chosen.** Verified before merge:
  - **P0-233-1** (no full inline Push dialog) — no `<Dialog>` shape was
    introduced; the only `Dialog` already present in the file is the
    pre-existing slice 099 row-detail drawer. ✓
  - **P0-233-2** (no `/v1/evidence:push` wire / backend touch) — code
    change confined to `web/app/(authed)/evidence/`. No backend or
    proto file touched. ✓
  - **P0-233-3** (no slice 204 audit harness touch) — no edits under
    `docs/audit-log/204-*` or related slice 204 scripts. ✓
- **Anti-criteria from prompt** (no `_STATUS.md` / `CHANGELOG.md` touch,
  no production code outside the evidence page, no in-app push UI) —
  verified by `git diff` review.
- **Confidence.** high.

## Revisit once in use

- **R1.** When slice 003's frontend follow-on (the in-app Push UI,
  Option B) ships, the `<a>` should either flip to a `<Button>` that
  opens the Dialog or stay as a "Read the docs" secondary link with
  the Dialog trigger as the new primary. The `push-cta.ts` constants
  can stay (the Dialog still needs to point at the doc for the
  expanded reference content).
- **R2.** When the empty-state CTA's `/admin/credentials` target gets
  resolved (the slice 099 honesty gap), that fix should also revisit
  whether THIS slice's destination should switch from the docs link
  to the credentials page. If both Option A and the
  `/admin/credentials` page co-exist, the action-bar CTA is the
  better surface for credentials and the inline subtitle link stays
  pointed at the docs.
- **R3.** Mockup update slice — if a maintainer wants the mockup to
  catch up to the live page (link instead of disabled button), file
  that as a small mockup slice (slice 220 pattern).

## Files touched

- `web/app/(authed)/evidence/page.tsx` — replace disabled `<Button>` with
  primary-styled `<a>`; introduce `subtitleNode` ReactNode and route all
  five ListPage branches through it; import the new helper.
- `web/app/(authed)/evidence/push-cta.ts` — new pure-logic module
  exporting `PUSH_CTA_LABEL`, `PUSH_CTA_HREF`,
  `PUSH_CTA_SUBTITLE_PREFIX`, and `pushCtaSubtitleSuffix()`.
- `web/app/(authed)/evidence/push-cta.test.ts` — new vitest spec; five
  cases covering label, href, prefix, non-disabled affordance, and
  the concatenated suffix.
- `web/e2e/evidence-list.spec.ts` — append a quarantined test block
  documenting the live render contract; body is commented per the
  file's existing seven-block convention.
- `docs/audit-log/233-decisions.md` — this file.

## Anti-criteria honored

- **P0-233-1.** No inline Push dialog ships. The action-bar element
  is a single `<a>` opening the canonical CLI doc; no textarea,
  no dialog, no upload form, no POST.
- **P0-233-2.** No `/v1/evidence:push` wire or backend handler change.
  No edits outside `web/`.
- **P0-233-3.** No slice 204 audit harness touch; no audit-script
  changes, no F-204-\* finding-list edits.
- **No `_STATUS.md` / `CHANGELOG.md` edits.** Verified by `git diff`.
- **No production code outside `/evidence`.** All five touched
  source files are under `web/app/(authed)/evidence/` or are docs.
- **No `@testing-library/react` introduced.** vitest config remains
  node-env / no-JSX.

## Constitutional invariants honored

- **Anti-pattern rejection (CLAUDE.md anti-patterns):** "Permanently-
  disabled CTAs without any textual cue about why they're disabled or
  what to do instead" — closed. The CTA now navigates to a real,
  reachable doc page.
- **Invariant 3 (Evidence SDK exposes one canonical inbound API).**
  Unchanged — this slice signposts the existing pathway honestly; the
  wire contract is untouched.
- **AI-assist boundary (CLAUDE.md):** not touched — no AI surface is
  introduced or modified by this slice.
- **Frontend testing discipline (slice 069 P0-A3):** vitest is node-
  env / no-JSX; this slice respects the rule by extracting a pure-
  logic module rather than introducing component rendering.

## Resolves

- **F-204-E-1** from `docs/audit-log/204-page-audit-evidence.md` — the
  next slice 204 audit run will no longer surface "Push evidence button
  disabled with no affordance" on /evidence.
