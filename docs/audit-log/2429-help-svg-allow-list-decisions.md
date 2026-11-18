# 2429 — Help diagram SVG check: parse and allow-list: decisions log

Issue: mgoodric/security-atlas#2429, a follow-up from the review of #2415
(#2148). Defence in depth: the diagrams are served through `<img>`, but
`public/help/diagrams/*.svg` can be opened directly on the app origin, where
an active part would run.

## Detection-tier classification

- **detection_tier_actual:** manual_review. The gaps were found by reading
  #2415, not by a test. Of the 19 fixtures this adds, 14 passed the #2415
  regex check (namespaced href, `<animate>`/`<set>`, every `@import` and
  `url()` case, an entity-encoded `javascript:` outside `href`, an XHTML
  element, a namespaced handler).
- **detection_tier_target:** unit (Node gate) and playwright (browser strip).

## Decisions

### D1 — a strict hand-written XML walker in Node, not a new dependency

The Node gate must parse, not match. `web/` has no XML parser, and adding one
needs a dependency approval (AGENTS.md "Ask before scaffolding"). The walker
in `diagram-build.ts` is small because it only has to accept what Mermaid
writes, and it fails closed on the rest: a DOCTYPE, any processing
instruction other than a leading XML declaration, an entity other than the
five predefined ones and character references, a duplicate or unquoted
attribute, mismatched or unclosed tags and text outside the root all make
the file unsafe. The cost is that a valid SVG using any of those is refused,
which for a generated diagram is the right way round.

### D2 — allow-list elements; rule-check attributes

Elements are allow-listed (`SVG_ELEMENTS`): Mermaid's state diagrams use 14
element names, and the list adds only static drawing and paint-server
elements. Attributes are not allow-listed: Mermaid writes `data-*`, ARIA and
many presentation attributes, and a list of them would break on any Mermaid
upgrade. Instead every attribute is judged by local name on its decoded
value: `on*` refused, `href` only to `#fragment`, `javascript:`/`vbscript:`
and non-fragment `url()` refused anywhere, and `@import`, CSS escapes and
script hooks refused in `style`. Namespace declarations are limited to SVG
and XLink, and the browser strip drops attributes in any other namespace.

### D2a — review fold-ins: escapes everywhere, `xml:base`, `image-set()`

Review of #2625 found that `fill="u\72l(https://…)"` passed both sides:
presentation attributes (`fill`, `mask`, `cursor`, `filter`, `clip-path`)
are parsed as CSS, so a CSS escape could spell `url(` there. A backslash is
now refused in every attribute value, not just `style`. Two more were
closed at the same time: every `xml:` attribute other than `xml:space` and
`xml:lang` is refused (`xml:base` would re-root a `#fragment` link onto
another origin), and `image-set(` is refused in style sheets and `style=""`
next to `@import`. Each has a fixture on both sides.

### D3 — `<animate>` and `<set>` are refused, not inspected

The ticket asks to refuse them when they target `href`. They are off the
allow-list altogether: lifecycle diagrams do not animate attributes, and
checking `attributeName` would miss `values=`, `from=`/`by=` and future
animation elements.

### D4 — the browser keeps its own copy of the list

`stripActiveSvg` runs in the page with no imports, so it cannot read
`SVG_ELEMENTS`. `e2e/help-svg-strip.spec.ts` strips an SVG holding every
listed element and checks each survives, so the two lists cannot drift
without a failing spec. The 19 hostile fixtures live in
`lib/help/unsafe-svg-fixtures.ts` and run against both sides.

### D5 — no render-pipeline bump

`RENDER_PIPELINE` is bumped when the pipeline would write a file
differently. The 16 committed SVGs contain nothing the new strip removes and
all pass the new gate, so their bytes and stamps stay valid and no re-render
is needed.

### D6 — `%%{` is rejected anywhere, and the renderer parses first

`parseStateDiagram` refuses a line that starts `%%{` (with or without space
after `%%`) or contains `%%{`, since Mermaid reads init directives before
the diagram type. `render-help-diagrams.mjs` now calls `parseStateDiagram`
on every block before rendering, so a directive never reaches Mermaid even
locally.
