# 2316 — Markdown follow-ups: decisions log

Issue: mgoodric/security-atlas#2316, follow-ups from the review of #2313
(#2094, the shared `<Markdown>` component, `web/lib/markdown.ts`).

## Detection-tier classification

- **detection_tier_actual:** manual_review. Both renderer bugs were found
  reviewing #2313.
- **detection_tier_target:** unit. The new `reference labels (#2316)` and
  `definitions inside block quotes (#2316)` vitest cases fail against the
  #2313 renderer (10 failures) and pass with this change.

## Decisions

### D1 — the evidence statement renders through `<Markdown>`

`/evidence/[id]` showed a manual attestation's statement in a
`whitespace-pre-wrap` span. It now goes through `<Markdown>`, like the other
long-text fields #2094 covered. An empty statement still shows the em dash
as text. The attestor and supporting link stay as they were: one is a name,
the other is already an allow-listed `<a>`.

### D2 — exception justification: already done

The ticket asked for it "once an exception detail view exists". That view
landed in #2323 (#2113) and already renders `ex.justification` through
`<Markdown>` (`web/app/(authed)/exceptions/[id]/page.tsx`). Nothing to
change; the list's 80-character preview stays plain text, per #2313.

### D3 — one label normalisation, on unescaped text, on both sides

The definition map was keyed on the raw label lowercased; the lookup used
the label from already-escaped inline text, so `Q&A` was looked up as
`q&amp;a` and never matched. Both sides now go through `normalizeLabel`,
and the lookup unescapes first. `normalizeLabel` follows CommonMark: trim,
collapse internal whitespace to one space, case fold. JavaScript has no
case-fold primitive; lower-then-upper covers the common gap a lone
`toLowerCase` leaves (`ß` vs `SS`). Full Unicode case folding is out of
scope.

Safety does not change: the target still goes through `safeHref`, and the
link text is still the escaped text, so a `<script>` label resolves to a
link whose text is inert (pinned by a test).

### D4 — definitions inside block quotes

The collection pass now strips leading `>` markers (up to the existing
`MAX_QUOTE_DEPTH`, the same pattern `renderBlocks` uses) before matching a
definition. A definition inside a quote is collected and its line is
reduced to its `>` markers, so the quote keeps its shape; a quote holding
only a definition renders as an empty `<blockquote>`, as CommonMark does.

Fence tracking had to learn quotes at the same time, or a definition-shaped
line inside a quoted fence would resolve. The pass records the quote depth
a fence opened at; the fence closes on a fence line at that depth, or when
the quote ends (which is where `renderBlocks` ends it too). A quote-shaped
line inside a top-level fence is still code.

### D5 — a seeded random-input property test

The component had no fuzz harness. A small seeded generator over
markdown- and HTML-significant fragments renders 2000 documents and checks
that every emitted tag is in the renderer's fixed grammar, every href
passes the allow-list, and no `<` survives outside a tag. Seeded, so a
failure reproduces.
