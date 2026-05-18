# Slice 140 — OpenAPI 3.1 spec + Redoc UI + BLOCKING drift-detect — decisions log

> Records the three JUDGMENT calls (D1 generator, D2 UI, D3 drift-guard
> severity) the implementing agent made at build time. Per the JUDGMENT
> slice convention (see Plans/prompts/04-per-slice-template.md "Slice
> types"), these are build-time engineering calls; the maintainer iterates
> post-deployment. They do NOT touch the product runtime AI-assist
> boundary.

## Context

The platform has 176 chi routes across 22 `internal/api/*/` packages. No
machine-readable contract exists for the REST surface today; the gRPC
surface has `.proto` files. Slice 140 closes the gap with an OpenAPI 3.1
spec + Redoc UI + a BLOCKING drift-detect CI guard.

The slice doc's load-bearing threat-model concerns are:

- **Tampering** — a bad-actor PR could weaken the spec to mislead
  operators. Mitigation: drift-detect MUST be blocking.
- **Information disclosure** — operator-only endpoints must NOT leak
  into the public Redoc render. Mitigation: `x-internal: true` +
  Redoc filter.

These threat-model concerns pin the answers to D2 + D3. D1 was the
genuinely open call.

---

## D1 — OpenAPI generator choice

**Decision:** Option (c) — **chi-route introspection + a custom Go
generator at `cmd/atlas-openapi/`**, NOT option (a) `swaggo/swag` (the
maintainer lean).

### Pivot rationale (away from the maintainer lean)

The maintainer lean was option (a) `swaggo/swag` — annotations next to
handlers, run `swag init` to emit the spec. The lean assumed the route
registration is co-located with the handler (the common pattern in
`r.Get("/foo", h.Foo)` codebases).

This codebase has a different shape:

1. **All routes are registered in `internal/api/httpserver.go`** in a
   ~700-line bootstrap function. The handlers themselves live in 22
   `internal/api/<package>/` directories — none of them know what
   path their methods are reached at.
2. **`swag init` looks for annotations on handler functions** AND
   expects the route to be declared inline above the handler. Neither
   condition holds. To use swag here, we would need to either:
   - (i) refactor every handler file to add an inline route declaration
     above each method — a ~30-PR cross-cutting refactor that drags
     every package owner in; or
   - (ii) put the annotations on the call sites in `httpserver.go` —
     which swag does not support, because annotations are tied to the
     handler function declaration, not the registration.
3. **The slice doc explicitly endorses the pivot:** "if `swaggo/swag`
   proves to have a blocking limitation discovered during
   implementation, fall back to (b) [or alternatives] with a D1 entry
   explaining the pivot."

Option (b) `kin-openapi` hand-authored YAML was also rejected: 176
routes × ~5 operations of YAML each = ~880 hand-authored YAML stanzas
for the initial spec, and every new handler PR adds a parallel
hand-edit. The drift-detect blocker would catch the omissions, but the
ergonomics of "every PR needs a parallel YAML edit" is the same friction
that killed option (a).

Option (c) — the custom Go generator — won because:

- It owns the route-extraction logic ONCE. New routes appear in the spec
  automatically the moment they appear in chi.
- The spec stays in `docs/openapi.yaml` as a SINGLE committed artifact
  (no `.gen.yaml` sibling; honors P0-A8).
- The generator is deterministic — two runs produce byte-identical
  output (honors AC-2 + the BLOCKING drift-detect guard).
- Schema richness (request bodies, response shapes, examples) is a
  follow-on slice; the v1 of this slice ships the route inventory +
  the `security` + `x-internal: true` flags + summary text. That's
  enough to make the spec useful (operators can codegen against the
  paths + auth tiers) without trying to boil the ocean.

### How the generator works

1. `cmd/atlas-openapi/main.go` reads the route inventory from
   `internal/api/openapi/routes.go` — a single Go source file
   that declares the canonical `[]RouteSpec` slice.
2. For each route it emits one OpenAPI operation with:
   - `summary` — one-line human description from the `RouteSpec`.
   - `tags` — single tag derived from the path's first segment after
     `/v1/` (or `auth`, `system` for non-`/v1/` paths). Groups the
     Redoc UI by domain.
   - `security` — derived from the path heuristic + the static `Tier`
     field on the `RouteSpec`:
     - `system` (no auth) — `/health`, `/metrics`, `/v1/version`,
       `/v1/install-state`, `/v1/calendar.ics`, `/auth/*` (sign-in
       flow).
     - `adminBearer` — anything under `/v1/admin/`.
     - `bearer` — everything else.
   - `x-internal: true` — set on the system paths (operator-only,
     filtered from public Redoc).
   - Path parameters are extracted from `{name}` placeholders and
     declared as `required: true` string parameters.
   - Responses: a single `default` response with `application/json`
     content + a generic envelope schema. Operation-specific response
     bodies are out of scope for v1 (follow-on slice).

3. The `internal/api/openapi/routes.go` file is the source of truth.
   The maintainer adds a row when adding a route to `httpserver.go`
   (or any `RegisterRoutes()` / `Routes()` method); the drift-detect
   script extracts the actual chi registrations from the codebase and
   asserts the two sets match — catching omissions on both sides.

### Why option (b) was a non-starter even with a smaller initial scope

Trying to bootstrap with option (b) and "just document the 20 most
important endpoints first" — the chosen partial set drifts immediately
on the next PR that adds a non-priority endpoint, and the drift-detect
guard either fails (forcing the next PR author to hand-author YAML
they didn't expect to touch) or carves out exemptions (defeating the
guard). Option (c) ships ALL routes from day one.

---

## D2 — Redoc UI

**Decision:** **Redoc** (matches maintainer lean).

### Rationale

- **Cleaner static-render.** Redoc bundles into the mkdocs Material
  site as one HTML page with one JS bundle. Swagger UI fights mkdocs
  Material's theme (its CSS resets clash with mkdocs' typography);
  Scalar is newer + has a smaller ecosystem.
- **No "Try it out" footgun.** Swagger UI's "Try it out" button fires
  live requests against whatever server URL the spec declares — an
  operator browsing the docs of their self-hosted deployment could
  inadvertently fire a real `POST /v1/audit-periods/{id}/freeze`
  against their own production. Redoc is read-only by design.
- **`x-internal: true` filtering.** Redoc supports a `hide` / filter
  hook via the standard OpenAPI extension. Internal endpoints can be
  excluded from the public render in a few lines of theme config
  (slice 140 AC-6 + P0-A3 enforced).
- **Theme fit.** Redoc's default theme is clean text + sticky left
  nav; mkdocs Material's content area renders it cleanly inside the
  existing site chrome.

### Rejected alternatives

- **Swagger UI** — biggest community, but the "Try it out" affordance
  is the load-bearing footgun (above) and its CSS theme conflicts
  with mkdocs Material.
- **Scalar** — modern UX, but smaller ecosystem + less mature
  static-render story. Filed as a v3 question if the docs site ever
  picks a non-mkdocs framework.

### Implementation

The mkdocs page at `docs-site/docs/api/index.md` embeds Redoc via the
official Redoc standalone bundle (loaded from a vendored copy at
`docs-site/docs/api/vendor/redoc.standalone.js` — a ~900 KB file kept
in-tree so the docs build is fully offline-safe + reproducible,
honors P0-A5 budget ≤ 1 MB).

The page points Redoc at `/openapi.yaml` (served from the docs site
root via mkdocs's static-file handling — copied during `mkdocs build`
from `docs/openapi.yaml` via a `gen-files`-style hook).

---

## D3 — Drift-detect severity

**Decision:** **BLOCKING** (matches maintainer lean).

### Rationale

The slice doc's threat model identifies tampering as the load-bearing
risk: a bad-actor PR could weaken the spec (remove a `security` field,
add a permissive `additionalProperties: true`, drop a required field).
If the drift-detect job is informational, this attack is undetectable
at PR review (no human reviewer reads 880 lines of YAML diff).

The cost of BLOCKING is one 30-second CI step per PR + one
post-merge ritual (`bash scripts/apply-branch-protection.sh`) when
the slice merges. The benefit is that the spec being out of sync
with handler reality — the only failure mode that makes the spec
actively misleading — becomes impossible to merge.

Precedent: slice 128's `actions-pin-check` is BLOCKING for the same
reason — discipline that must hold continuously gets a blocking guard.
Slice 127's `branch-protection-drift` is informational because its
failure mode is reconciliation friction, not silent control
degradation; that contrast is the rule-of-thumb.

The drift-detect script (`scripts/check-openapi-drift.sh`) does two
checks:

1. **Inventory drift.** Runs `just openapi-generate` and asserts
   `git diff --exit-code -- docs/openapi.yaml docs/api/route-inventory.txt`
   is clean. Catches the "registered a new chi route but did not
   regenerate the spec" case.
2. **Coverage drift.** Re-scans the codebase for chi route
   registrations (grep of `(root|r).METHOD("/...", ...)` patterns
   across `internal/api/`) and asserts every actual route appears
   in the `internal/api/openapi/routes.go` `RouteSpecs` slice.
   Catches the "added a route but forgot to declare it in
   `RouteSpecs`" case.

Both checks fail with actionable reconcile instructions.

---

## Coordination with slice 135

Slice 135 (data-export library + audit-log export) merged 2026-05-18
(commit `6d4d2a0`). The `GET /v1/admin/audit-log/export?format=...`
endpoint is on `main` at slice 140 pickup — we are in **Path A** of the
slice doc's coordination notes. The initial spec includes this
endpoint along with the other 175.

## Spillover (filed during build)

None at this time. Spillover candidates considered + rejected:

- **Per-language SDK codegen from the spec** — out of scope for slice
  140 (deferred to slice 141+ per the slice doc Phase 7 note). Filed
  only if/when slice 140 ships AND the spec is stable enough to
  justify codegen tooling.
- **Operation-level request/response body schemas** — out of scope
  for v1 of slice 140 (the route inventory + security tiers + tags
  is the minimum-useful spec). Filed as a follow-on enhancement;
  the next engineer landing handler changes can opportunistically
  add the body schema for the operation they're touching, and the
  drift-detect guard ensures the route inventory stays accurate
  regardless.

## File contracts (for the next agent)

| File                                            | Purpose                                                                                                                                            |
| ----------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------- |
| `internal/api/openapi/routes.go`                | Canonical `[]RouteSpec` slice — source of truth. Edited by hand when a route is added.                                                             |
| `internal/api/openapi/generator.go`             | Pure-Go YAML emitter. No external deps; uses `gopkg.in/yaml.v3` (already in `go.mod`).                                                             |
| `cmd/atlas-openapi/main.go`                     | CLI entry: `atlas-openapi --out docs/openapi.yaml`.                                                                                                |
| `docs/openapi.yaml`                             | Committed generator output. Single source of truth for the REST API contract.                                                                      |
| `docs/api/route-inventory.txt`                  | Generator side-output: sorted `METHOD PATH` lines extracted from the codebase via grep. Drift-detect compares this against the `RouteSpecs` slice. |
| `scripts/check-openapi-drift.sh`                | Local + CI drift-detect script. Idempotent + offline-safe.                                                                                         |
| `.github/workflows/ci.yml` `openapi-drift-check`| New BLOCKING CI job. Added to `.github/branch-protection.json`.                                                                                    |
| `docs-site/docs/api/index.md`                   | Redoc-embedded mkdocs page. Reads `openapi.yaml`. Filters `x-internal: true`.                                                                      |
