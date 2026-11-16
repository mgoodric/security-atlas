# #2143 — help content pipeline and the "How this works" panel — decisions log

**Ticket:** https://github.com/mgoodric/security-atlas/issues/2143 (parent #2125)
**Shape:** one Markdown file per module at `web/help/modules/<id>.md`, with front
matter (`id`, `title`, `routes`, `terms`, `diagrams`). `web/lib/help/modules.ts`
imports each file's text at build time, parses it, and maps a page's path to its
module. PageShell renders `<HelpButton>` (`web/components/help/help-panel.tsx`),
a **?** that opens a modal right-hand Sheet with the help, its key terms and
"Read more". The `docs-site/hooks/help_pages.py` mkdocs hook publishes the same
files as `help/<id>.md`. Controls is the first module.

detection_tier_actual: manual_review
detection_tier_target: playwright

(The one bug found: the first draft of the e2e focus-trap check read
`document.activeElement` right after each Tab and failed spuriously, because
base-ui's focus guard hands focus back a tick later. Found by a local run
against a stubbed API before CI. The spec now polls. See D5.)

## D1 — the `.md` files are imported as text, not generated into TS

Turbopack (Next 16's default bundler) has a built-in `raw` module type, so
`turbopack.rules["*.md"] = { type: "raw" }` in `next.config.ts` bundles each
file's text with no new dependency and no generated file to drift. Vitest gets a
five-line `help-markdown` transform plugin that does the same, and
`web/help/markdown.d.ts` types the import. The alternative, a committed
`modules.generated.ts` plus a drift test, adds a second copy of every help file.

## D2 — a small front-matter subset, parsed by hand

The web app ships no YAML parser and the help needs only scalars, flow lists and
a list of `{src, alt}` maps. `parseHelpModule` reads exactly that and throws on
anything else (an unknown key, a repeated key, a diagram without alt text). It
also accepts a flow list Prettier wrapped onto the line after its key, because
the pre-commit Prettier does exactly that to a long `terms:` list. The docs hook
reads the same files with PyYAML, which mkdocs already depends on.

## D3 — route mapping is longest-prefix over the module's own `routes`

A module's route covers the page and everything below it (`/controls` covers
`/controls/{id}`), and the longest matching route wins, so a later module can
claim `/controls/checklist` without editing Controls. A page no route covers
returns no module, and PageShell renders no **?** there. Enforcing coverage of
every page is a later ticket.

Module ids use the `internal/featureflag.Modules` key where the module is an
optional one, so a switched-off module's help lines up with its gate. Controls
is core and has no registry key, so its id is simply `controls`.

## D4 — key terms reference `terms.json`, links are absolute

The front matter lists term ids; the panel and the docs page both read the name
and short definition from `web/help/terms.json`, so no definition is copied.
`modules.test.ts` fails on an unknown id. The shared `<Markdown>` renderer drops
relative links by design, so "Read more" is built outside the Markdown and
points at the absolute docs URL, and the test fails on any non-`https` link in a
help body. The same test runs the `no-dev-notes-in-ui` patterns (now exported as
`findDevNote`) over every help file.

## D5 — the panel is the existing Sheet, not a new dialog

`components/ui/sheet.tsx` wraps the base-ui Dialog, which already provides the
modal semantics, focus move, focus trap, Escape and focus restore. The panel
only adds `aria-modal="true"` explicitly so the spec can assert it. The trap's
last step goes through a focus-guard element that redirects asynchronously, so
`e2e/help-panel.spec.ts` polls after each Tab instead of reading the active
element at once.

## D6 — diagrams are images, never inline SVG

A diagram is a file under `web/public/help/diagrams/`, rendered as
`<img src alt>`, and copied next to the docs page by the hook. No SVG markup
from a help file reaches the DOM. Controls has no diagram yet; the parser,
the existence check and the hook's copy step are in place for the first one.
