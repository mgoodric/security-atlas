# 2394 — Web BFF types generated from OpenAPI (remap first): decisions log

Issue: mgoodric/security-atlas#2394, from the review of #2393 (#1778). The
remap page's response types and its Playwright fixture were hand-written
against each other, so a Go shape change failed neither typecheck nor the
mocked spec.

## Detection-tier classification

- **detection_tier_actual:** unit (typecheck). Wiring the generated types into
  the `/controls/remap` fixture failed the typecheck at once: the edge
  proposal was missing six fields the platform sends, and the mocked decision
  and bulk bodies were missing most of theirs. The list mock also lacked
  `total`.
- **detection_tier_target:** unit (typecheck). That is the tier this change
  puts the contract in.

## Decisions

### D1 — the spec's schemas are reflected from the Go wire structs

`docs/openapi.yaml` documented every remap response as the empty `Ack`, so
types generated from it would have caught nothing. Hand-writing schemas in the
generator would only have moved the hand-written contract. Instead
`internal/api/openapi/schema.go` reflects each struct listed in
`ResponseSchemas` (`responses.go`) the way `encoding/json` encodes it: the tag
name, `omitempty` makes a field optional, `-` skips it, and nested structs
become `$ref`s. A Go type it does not know is an error, not a guess. Enums
come from the Go value lists (`scfimport.RemapClasses`,
`control.RemapStatuses`, `crosswalkremap.Statuses`), so `class` and `status`
are unions in TypeScript.

The chain a renamed JSON field now breaks: `openapi-drift-check`, then
`openapi:check` in `Frontend · install + build`, then `npm run typecheck`
wherever the old name is read. `controls.remapProposalWire` and
`remapProposalsResp` were exported (as `RemapProposal` and
`RemapProposalsResponse`) so the generator can name them.

A nil Go slice encodes as `null`, which the schema does not admit. The
handlers covered here allocate every slice (`[]X{}`), so the arrays are
documented as never null.

### D2 — the generated file is committed and checked, not built at typecheck

`web/lib/api/generated/openapi.d.ts` is committed, like `docs/openapi.yaml`.
`npm run openapi:check` regenerates it in memory and fails on any difference.
That keeps `npm run typecheck` and `next build` free of the generator, makes
the contract diff visible in review, and mirrors the Go-side drift check. The
file is excluded from prettier and eslint, since the check compares it byte
for byte.

### D3 — openapi-typescript sits in its own package

`openapi-typescript@7.13.0`, the latest release, declares a `typescript@^5.x`
peer. Web is on TypeScript 6, npm refuses the install, and `overrides` cannot
widen a peer range. Installing with `--legacy-peer-deps` reshuffled the root
lockfile and would make every later plain `npm install`, Dependabot's
included, hit ERESOLVE. So `web/tools/openapi-types/` is a separate package
with its own lockfile and `.nvmrc`, pinning `openapi-typescript@7.13.0` and
`typescript@5.9.3`. TypeScript 5 there only prints the file, and web compiles
it with TypeScript 6. CI runs `npm ci --prefix web/tools/openapi-types`.
Dependabot has an entry for the directory that holds the TypeScript major. Web's
tsconfigs exclude `tools/`. The README there says when to fold it back.

### D4 — the anchor responses stay hand-written

`AnchorLookupResponse` and `AnchorSearchResponse` are built by the BFF route
(`app/api/controls/remap-proposals/anchors/route.ts`), not returned by the
platform, so there is no platform schema to generate them from.

### D5 — the two-release seed harness is #2392's

The ticket offered a real-stack seed harness as an alternative or addition.
It is tracked in #2392 and left out here.

### D6 — the remaining modules move incrementally

Remap is the only module moved. Each follow-up adds its operations to
`ResponseSchemas`, regenerates both files, and swaps the module's
hand-written interfaces for `components["schemas"]` aliases. The modules
under `web/lib/api/` that fetch the platform through `_shared` and still
hand-write its shapes: `admin`, `anchors`, `audited-under`, `calendar`,
`control-detail`, `control-imports`, `controls-list`, `csf`, `dashboard`,
`evidence-kinds`, `framework-scopes`, `metrics`, `needs-attention`,
`oscal-components`, `policies`, `portfolio-summary`, `reanchoring`,
`requirement-coverage`, `risk-hierarchy`, `risks`, `saved-views`,
`scope-dimensions`, `self.server` and `vendors`. The rest of the directory
needs the same audit.
