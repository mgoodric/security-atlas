# 252 — Settings admin cross-link renders ASCII "->" instead of Unicode "→"

**Cluster:** Frontend
**Estimate:** 0.05d
**Type:** AFK
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)
**Parent:** #204 (per-page UI parity audit fleet) — settings audit. Slice 154 settings-only audit did not call out this specific glyph delta; it's surfaceable only by a side-by-side mockup-vs-live pixel/text compare.

## Narrative

`Plans/mockups/settings.html` line 109 (the page subhead):

```html
Tenant-wide settings live in
<a href="#" class="text-brand-600 hover:text-brand-700"
  >Tenant administration → /admin</a
>
(admin role required).
```

The mockup uses **Unicode RIGHTWARDS ARROW** `→` (U+2192).

Live page in `web/app/(authed)/settings/page.tsx:192`:

```tsx
Tenant administration ({"->"}/admin)
```

This renders as the literal three-character ASCII string `->` —
NOT the Unicode arrow. The fallback for non-admin (line 196) also
omits the arrow entirely. The link parens around the path
(`(→/admin)` vs the mockup's `→ /admin`) are also slightly
different in spacing.

**Why this matters:**

1. **Mockup parity** (slice 204 audit category-i — layout/chrome).
   The mockup is the design spec; the ASCII fallback was a hasty
   choice during slice 103 (the original settings slice) likely
   to avoid Unicode-in-JSX gotchas. Other pages of the platform
   render `→` happily; settings is the outlier.
2. **Typographic polish.** `→` is the symbol the rest of the
   product uses; mixing `->` reads as a paste-from-terminal
   artifact in the middle of polished prose.
3. **Localization (future).** A Unicode arrow is locale-neutral;
   `->` reads as ASCII fallback for systems that lack font
   support — a non-issue in 2026, but the inconsistency is real.

**Fix:** swap line 192 of `web/app/(authed)/settings/page.tsx`:

```tsx
// from
Tenant administration ({"->"}/admin)
// to
Tenant administration → /admin
```

And confirm the spacing matches mockup line 109 (space-arrow-space
inside the link text). One-line cosmetic fix.

## Threat model

**Verdict.** `no-mitigations-needed`. Cosmetic copy change. No
security surface, no data binding.

## Acceptance criteria

- **AC-1.** `web/app/(authed)/settings/page.tsx:192` renders the
  literal Unicode arrow `→` (U+2192) between "Tenant
  administration" and "/admin".
- **AC-2.** Mockup parity: the rendered string matches
  `Plans/mockups/settings.html` line 109's "Tenant administration
  → /admin" exactly (mod the surrounding sentence punctuation
  preserved from the live impl).
- **AC-3.** No regression in `data-testid="settings-admin-cross-link"`
  selector visibility — Playwright spec
  `web/e2e/settings.spec.ts` still finds the link.
- **AC-4.** Pre-commit + CI green.

## Constitutional invariants honored

- **Article VII (Simplicity Gate).** A one-line cosmetic fix.

## Canvas references

- `Plans/mockups/settings.html` line 109 — Unicode arrow in the
  subhead link copy.

## Dependencies

- **#204** (this slice's parent).
- **#103** (settings page initial slice) — merged; this is a
  micro-correction.

## Anti-criteria (P0 — block merge)

- **P0-252-1.** Does NOT change the `href` of the admin cross-link.
- **P0-252-2.** Does NOT change the data-testid attribute.
- **P0-252-3.** Does NOT change the non-admin fallback copy from
  slice 154 / 249's scope (only the admin variant glyph).
- **P0-252-4.** Does NOT introduce a global arrow-glyph helper
  ("just inline the character"). A future slice can extract if
  more pages have the same ASCII fallback.

## Skill mix (1)

1. JSX text-content edit + Playwright selector regression.
