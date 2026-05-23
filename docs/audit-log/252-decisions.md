# 252 — Settings admin cross-link Unicode arrow · decisions log

**Slice:** `docs/issues/252-settings-admin-link-uses-ascii-arrow-not-unicode.md`
**Branch:** `frontend/252-settings-unicode-arrow`
**Author:** Claude (engineer subagent)
**Date:** 2026-05-23

One-character cosmetic fix per the spec's exact direction (line 49-56 of
the slice doc). No design discretion required.

---

## Decisions made

### D1 — Use the literal inline `→` character, not an HTML entity or helper

**Decision:** **Replace `{"->"}` with the inline UTF-8 character `→`
(U+2192, bytes `e2 86 92`) directly in JSX text content** at
`web/app/(authed)/settings/page.tsx:192`. The new line reads
`Tenant administration → /admin` exactly as the spec's fix block (line
54-55) prescribes — no parentheses, single spaces flanking the arrow.

**Options considered:**

| Option                                | Why rejected / why chosen                                                                                                                                                                                |
| ------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| (a) **Inline Unicode `→`** — _chosen_ | Spec line 54-55 prescribes this exact string. Other pages already render `→` inline (no Unicode-in-JSX issues observed in the codebase). Anti-criterion P0-252-4 explicitly forbids extracting a helper. |
| (b) **HTML entity `&rarr;`**          | Rejected — JSX would render the literal text `&rarr;`, not the arrow, unless wrapped in `dangerouslySetInnerHTML`. Worse than ASCII.                                                                     |
| (c) **JS expression `{"→"}`**         | Rejected — functionally identical to (a) but adds visual noise that mirrors the original `{"->"}` workaround we're explicitly walking back.                                                              |

**Confidence:** **high.** Spec prescribes the exact replacement string.

### D2 — Parentheses around `/admin` dropped per spec

**Decision:** **Remove the parens around `/admin`.** The original
rendered `Tenant administration (->{path})` with the path inside parens;
the spec's fix block (lines 54-55) shows the new form with no parens:
`Tenant administration → /admin`. AC-2 confirms this matches mockup
line 109 ("Tenant administration → /admin").

**Confidence:** **high.** Spec is unambiguous.

### D3 — Line 47 prose comment left untouched

**Decision:** **Do NOT update the `// Cross-link "Tenant administration
-> /admin" is visible only to admins` comment at line 47.** The comment
is descriptive prose about what the page renders; updating it to
`-> → →` is mechanical correctness but the orchestrator's
anti-criterion is explicit: "NO replacement of `->` anywhere else in
the codebase (only the settings cross-link string)". A comment is not
a user-facing string. Treating "the settings cross-link string" as the
rendered JSX text-content only.

**Confidence:** **high.** Anti-criterion is the constraint.

---

## Anti-criteria honored

- **P0-252-1.** href `/admin` unchanged.
- **P0-252-2.** `data-testid="settings-admin-cross-link"` unchanged.
- **P0-252-3.** Non-admin fallback copy on line 196 unchanged.
- **P0-252-4.** No global arrow-glyph helper introduced — the character
  is inlined.

Orchestrator anti-criteria also honored: no other `->` instances
modified; no `_STATUS.md` / `CHANGELOG.md` touched.

---

## Confidence summary

| Decision                          | Confidence |
| --------------------------------- | ---------- |
| D1 — inline Unicode `→`           | **high**   |
| D2 — parentheses dropped per spec | **high**   |
| D3 — line 47 comment untouched    | **high**   |

No medium- or low-confidence decisions. Every choice is spec-prescribed.
