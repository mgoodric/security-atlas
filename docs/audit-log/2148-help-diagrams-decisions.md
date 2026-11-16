# #2148 — lifecycle diagram build and the diagram check — decisions log

**Ticket:** https://github.com/mgoodric/security-atlas/issues/2148 (parent #2125)
**Shape:** a help file embeds a lifecycle as a ` ```mermaid ` block whose
first line is `%% lifecycle: <name>`. `web/lib/help/lifecycle-diagram.ts` parses
it (client-safe, no Mermaid); `web/scripts/render-help-diagrams.mjs`
(`just help-diagrams`) pre-renders a light and a dark SVG into
`web/public/help/diagrams/`; `web/lib/help/diagrams.test.ts` is the diagram
check; `web/scripts/check-no-mermaid-bundle.mjs` is the bundle check, run after
`next build` in `Frontend · install + build`. The Controls help gains the
exception lifecycle.

detection_tier_actual: none
detection_tier_target: unit

## D1 — Mermaid runs in the Playwright Chromium, not through mermaid-cli

`@mermaid-js/mermaid-cli` drives Puppeteer, which downloads its own Chromium.
The repo already installs one browser, for Playwright. The render script loads
the pinned `mermaid` devDependency's `dist/mermaid.min.js` into a page of that
Chromium and calls `mermaid.render`, which is all mermaid-cli does. One browser,
one pinned diagram package (`"mermaid": "12.2.0"`, exact, devDependencies only;
the check asserts both).

## D2 — the SVGs are committed, and the check compares stamps, not bytes

"Pre-rendered at build time" could mean rendering inside `next build`. That
would put a headless browser into every web build, including the Docker image
builds. Instead the SVGs are rendered by a build step and committed, and each
carries a sha256 stamp over everything that shapes it: the Mermaid version, the
theme's token values, the token-to-Mermaid mapping, and the diagram source.
The check recomputes the stamp and fails on a stale or missing file, or a file
no help file draws. Comparing SVG bytes would be flaky: Mermaid measures label
text with the rendering machine's fonts, so macOS and Linux produce different
geometry for the same input.

## D3 — the diagram is compared with the exported JSON, by a strict parser

The check reads `docs/lifecycles/<name>.json`, which `cmd/atlas-lifecycles`
exports from the declared transition tables (#2146); nothing is hand-copied.
It compares states, initial states with their action, terminal states, and
transitions with their action. A system-actor move must be labelled
`action (system)`, and the check fails when the label and the table's `actor`
disagree, so the diagram cannot present an automatic move as a person's. The
parser accepts only the header, `direction`, `%%` comments and `A --> B: action`
edges, and throws on anything else (a `state` declaration, a note, a `click`
directive), so no line can draw something the check did not compare.

`docs/lifecycles/**` joins the CI `code` filter and the `shared` tier set, so a
change to an exported lifecycle runs the web check even with no web file in the
diff.

## D4 — light and dark are two images, switched by the app's `dark` class

An SVG shown through `<img>` cannot read the page's CSS variables, and the app's
theme is a `.dark` class (the Settings picker), not only the media query. So
each diagram is rendered twice, from the `:root` and `.dark` token blocks of
`globals.css` (oklch resolved to hex in the browser, since Mermaid's colour
maths reads hex/rgb/hsl), and the panel shows one with `dark:hidden` and the
other with `hidden dark:block`. The hidden one is `display: none`, so it is out
of the accessibility tree as well. The docs site uses Material's `#only-light`
/ `#only-dark` for the same pair.

## D5 — the text alternative is the parsed diagram, as a described-by list

The image's `alt` is short ("Exception lifecycle: 6 states and 5 transitions,
listed below.") and `aria-describedby` points at the full list of states
(with "starts here" and "final" marks) and transitions (with "done
automatically by the system" for system moves), inside a "The diagram as text"
disclosure. The list is generated from the same parse the check verified, so
it cannot drift from the picture or the backend. The docs page prints the same
list.

## D6 — SVG safety

The SVGs are static files served through `<img>`, never inlined: no
`dangerouslySetInnerHTML`. The renderer runs Mermaid with
`securityLevel: "strict"` and `htmlLabels: false`, then `sanitizeSvg` strips
`<script>`, `on*` handlers and any link that is not a same-document fragment,
and refuses `<foreignObject>`. The check runs `unsafeSvgParts` over every
committed SVG, and a unit test feeds the sanitiser a hostile SVG.

## D7 — the bundle check matches Mermaid runtime signatures, not the word

The help Markdown is bundled as text, so the client bundle legitimately
contains "mermaid" and "stateDiagram-v2". The check looks for strings from
Mermaid's public API and error renderer (`registerExternalDiagrams`,
`Syntax error in text`), and its test proves they match Mermaid's own IIFE,
ESM and core dist files, so an upgrade that drops them fails the test before
the check goes blind.

## D8 — the exception diagram goes in the Controls help for now

The Playwright criterion needs a real diagram in a real panel, and Controls is
the only module with help today. Its body already explains "Excepted", so it
gains a short paragraph and the exception lifecycle. #2150 (Controls section
content) adds an Exceptions module and can move the diagram there.
