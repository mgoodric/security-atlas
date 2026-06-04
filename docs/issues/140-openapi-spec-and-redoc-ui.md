# 140 — OpenAPI 3.1 spec + Redoc UI + drift-detect CI guard

**Cluster:** Backend / Docs / Infra
**Estimate:** 2-3d
**Type:** JUDGMENT (generator choice + UI choice + drift-guard severity; engineer records D1-D3)
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Surfaced 2026-05-18 via `/idea-to-slice` from a maintainer-driven API-discoverability gap. The platform now has ~30 REST endpoints across 19 `internal/api/*/` packages — none have a machine-readable contract. The gRPC surface has `.proto` files (`proto/connectors/`, `proto/evidence/`, `proto/admin/`, `proto/oscal/`); the REST surface has only Go handler source code. Tech stack (canvas §9) commits to "REST `POST /v1/evidence:push` + gRPC streaming + CLI + per-language SDKs (Go, Python, TypeScript, Java)" but does NOT commit to OpenAPI as a deliverable — this slice closes that gap.

Triggered now because (a) the slice-135 export library (in flight, PR #292) adds another HTTP family (`/v1/admin/<entity>/export?format=...`) that deserves to land in a spec from day 1, not bolted on later; (b) connector authors writing in non-Go languages need codegen against the push API; (c) operators integrating against `/v1/admin/*` endpoints have no schema to validate against.

**What this slice ships:** an OpenAPI 3.1 spec generated from chi route metadata + handler annotations, hosted as a Redoc UI page on the mkdocs Material site (slice 058), validated against handler reality via a blocking CI drift-detect job (slice 109/127/128 pattern). The spec is the single source of truth for the REST API contract; the gRPC surface stays in `.proto`.

**Scope discipline (what is OUT):**

- **gRPC surface** — already specified in `.proto`; no OpenAPI for it. The two surfaces stay separately specified.
- **Per-language SDK codegen** — filed as spillover slice 141; gated on this slice's spec landing.
- **API versioning policy** (v1 → v2 migration shape) — out of scope; deferred to a v3 follow-on if v2 ever materializes.
- **Spec authoring for endpoints that don't yet exist** — only documents endpoints that ship on `main`. Slice 135's export endpoints (still in PR) get added in slice 135's own PR (the engineer there extends the spec as part of shipping each new endpoint).
- **Mock-server-from-spec** — out of scope. Spec is for documentation + codegen, not for mock-driven testing.
- **gRPC ↔ OpenAPI unification** — out of scope. The grpc-gateway pattern is a v3 question; not picked here.

## Threat model

| STRIDE                       | Threat                                                                                                                                                                                                                                                                                                                          | Mitigation                                                                                                                                                                                                                                                                                                                        |
| ---------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **S** Spoofing               | OpenAPI spec MAY inadvertently document endpoints WITHOUT their auth requirements (the `security` field) — leading an integrator to send unauthenticated calls and getting 401s on every request. Worse: documenting an endpoint as `security: []` (public) when it's actually authenticated misleads operators about exposure. | Every operation in the spec MUST carry a `security` field. The drift-detect job verifies every endpoint with a non-empty handler-side auth check has a matching `security` entry in the spec. P0-A1 enforces.                                                                                                                     |
| **T** Tampering              | A bad-actor PR could weaken the spec (remove a `security` field, add a permissive `additionalProperties: true`, drop a required field) to mislead future operators. The spec lives in `docs/openapi.yaml` (or similar); if drift-detect is informational not blocking, this attack is undetectable.                             | Drift-detect CI job is **BLOCKING** (slice 128's `actions-pin-check` pattern, NOT slice 109/127's informational pattern). Spec changes require handler-side changes (or vice versa); the two stay in lockstep. P0-A2 enforces.                                                                                                    |
| **R** Repudiation            | n/a — spec is build-time artifact, no runtime audit                                                                                                                                                                                                                                                                             | n/a                                                                                                                                                                                                                                                                                                                               |
| **I** Information disclosure | **Two leakage vectors.** (a) Spec MAY document internal endpoints (e.g. `/health`, `/v1/install-state`, debug endpoints) that should be marked `x-internal: true` and excluded from the public Redoc UI. (b) Example responses in the spec MAY include real-looking data; same threat model as slice 132's screenshot work.     | Internal endpoints carry `x-internal: true` extension; Redoc UI filters them out of the public render. Examples use neutral fixture values only (`test-actor-id-1`, `test@example.com`, etc.) — same convention as the rest of the codebase (slice 005 P0-A4). P0-A3 + P0-A4 enforce.                                             |
| **D** DoS                    | A spec file with overly-large examples / deeply-nested schemas can bloat the Redoc bundle, slowing the docs site. Spec generation MAY also be slow if scanning every handler file on every CI run.                                                                                                                              | Spec file size budget: ≤ 500 KB. Generator caches handler-scan output between runs. Redoc bundle is the single artifact loaded by the docs page; mkdocs-Material's lazy-load behavior covers concurrency. P0-A5 enforces.                                                                                                         |
| **E** Elevation of privilege | A spec that documents an admin-only endpoint as accepting a non-admin role (e.g. `security: [{bearer: []}]` instead of `security: [{adminBearer: []}]`) misleads about the role boundary. The role check is in the OPA gate, not the spec — but the spec is what operators read.                                                | Spec MUST distinguish auth schemes by role tier. Minimum: `bearer` (any authenticated), `adminBearer` (admin-only role gate). Optional: `auditorBearer`, `grcEngineerBearer` if the admit set is narrower than admin. Drift-detect verifies the handler's OPA gate name maps to the documented `security` scheme. P0-A6 enforces. |

**Verdict:** HAS-MITIGATIONS — the load-bearing risks are tampering (drift-detect must be blocking, not informational) and information disclosure (internal endpoints excluded from public UI). P0-A2 + P0-A3 are merge-blocking.

## Acceptance criteria

### Generator (D1 JUDGMENT call)

- [ ] **AC-1:** D1 recorded in `docs/audit-log/140-openapi-spec-decisions.md` picking ONE of: (a) `swaggo/swag` annotations on handlers (most popular; chi-compatible; annotations clutter handler files); (b) `getkin/kin-openapi` hand-authored YAML + runtime validation (cleanest spec; manual upkeep risk; drift-detect is load-bearing here); (c) chi-route introspection + custom Go generator (no annotations; precise on route table but imprecise on request/response schemas). Maintainer lean: **(a) `swaggo/swag`** — co-located source of truth, low ceremony, chi-ecosystem standard. The two rejected options' rationales are documented in D1.
- [ ] **AC-2:** Generator wired into `justfile` as `just openapi-generate`. Reproducible: two runs against the same `internal/api/` tree produce byte-identical output.
- [ ] **AC-3:** Generator output committed as `docs/openapi.yaml` (or `docs/api/openapi.yaml`). Single source of truth file; no auto-generated `.gen.yaml` sibling.

### Spec content

- [ ] **AC-4:** Every chi route exposed by `internal/api/httpserver.go` appears in the spec. Drift-detect (AC-9) enforces this continuously.
- [ ] **AC-5:** Every operation carries a `security` field. Authentication tiers minimum: `bearer` (any authenticated user), `adminBearer` (admin-only). Engineer adds narrower tiers (`auditorBearer`, `grcEngineerBearer`) if the admit set warrants — coordinate with slice 124 D5 + slice 130's OPA gates.
- [ ] **AC-6:** Internal endpoints (`/health`, `/metrics`, `/v1/version`, `/v1/install-state`, plus any other operator-only endpoint) carry the `x-internal: true` extension. Redoc UI filters these from the public render.
- [ ] **AC-7:** Example values use neutral fixture tokens only (no vendor prefixes, no real emails, no real tenant names). P0-A4 enforces.
- [ ] **AC-8:** Spec validates clean against the OpenAPI 3.1 schema (`swagger-cli validate` or equivalent). CI job runs this as a separate informational check.

### CI drift-detect guard (D3 JUDGMENT — blocking vs informational)

- [ ] **AC-9:** D3 recorded in decisions log: drift-detect is **BLOCKING** (slice 128's `actions-pin-check` pattern, NOT slice 109/127's informational pattern). Justification: the spec being out of sync with handler reality is the only failure mode that makes the spec actively misleading; the cost of a 30-second CI re-run is far cheaper than an operator silently coding against a stale contract.
- [ ] **AC-10:** NEW `.github/workflows/ci.yml` job `openapi-drift-check` that runs `just openapi-generate` and asserts `git diff --exit-code -- docs/openapi.yaml` is clean. Adds `openapi-drift-check` to `.github/branch-protection.json` required-checks contexts. Operator post-merge ritual: `bash scripts/apply-branch-protection.sh` (slice 127's apply script — same convention as slice 128).
- [ ] **AC-11:** NEW `scripts/check-openapi-drift.sh` that runs the same check locally. Idempotent + offline-safe.

### Redoc UI hosted in mkdocs (D2 JUDGMENT call)

- [ ] **AC-12:** D2 recorded in decisions log picking ONE of: (a) Swagger UI (most familiar; clutters docs site with its own theme); (b) **Redoc** (cleaner; well-themed for embedded use; static-render-friendly — maintainer lean); (c) Scalar (newest; modern UX; smaller community + ecosystem risk). The two rejected options' rationales are documented.
- [ ] **AC-13:** NEW mkdocs page `docs/api/index.md` (or similar) that embeds the Redoc renderer pointing at `docs/openapi.yaml`. Renders inside the mkdocs Material theme (slice 058) without theme conflicts. Internal endpoints filtered (AC-6).
- [ ] **AC-14:** mkdocs build (`just docs-build` or equivalent) succeeds with the new page. The Redoc bundle adds ≤ 1 MB to the published site (P0-A5 budget).

### Docs + CHANGELOG

- [ ] **AC-15:** CONTRIBUTING.md adds an "API spec" subsection documenting: when to update the spec (every handler change), how to regenerate (`just openapi-generate`), the drift-detect blocking guard, the operator post-merge ritual for branch-protection.
- [ ] **AC-16:** CHANGELOG entry under `[Unreleased] / Added`: "OpenAPI 3.1 spec for the REST API + Redoc UI on the docs site + BLOCKING openapi-drift-check CI guard (#140)".

## Constitutional invariants honored

- **Tech stack §9** — REST API surface is one of the tech-stack commitments; this slice closes the discoverability gap.
- **#9 Manual evidence is first-class.** Operators are first-class consumers of the platform; they deserve a machine-readable contract for integration.
- **AI-assist boundary.** Spec is deterministically generated; no AI authorship.
- The OpenAPI spec does NOT replace any audit-binding artifact. OSCAL stays the audit-binding wire format (canvas §3.4 + §8.2 + slice 030); OpenAPI is operator-discoverability only.

## Canvas references

- `Plans/canvas/09-tech-stack.md` — commits to gRPC + REST + per-language SDKs but does NOT commit to OpenAPI. This slice adds the OpenAPI commitment.
- `Plans/canvas/04-evidence-engine.md` — Evidence SDK push profile + gRPC profile. The push profile is HTTP-fronted; the OpenAPI spec is the operator-facing complement to the `.proto` file.
- `Plans/EVIDENCE_SDK.md` — full Evidence SDK contract. After this slice, the push surface has both `.proto` (for gRPC codegen) and OpenAPI (for HTTP codegen).
- `Plans/canvas/11-open-questions.md` item 20 (RESOLVED 2026-05-14): mkdocs Material is the docs site — the Redoc UI lives inside that scaffold.

## Dependencies

- **#003** Evidence SDK proto + push client + CLI (merged) — the `.proto` source of truth for the gRPC surface; this slice does the complement for REST.
- **#058** User-docs scaffold / mkdocs Material (merged) — the Redoc UI lives inside this site.
- **#127** Branch-protection drift fix + apply ritual scripts (merged) — provides `scripts/apply-branch-protection.sh` that the operator post-merge ritual uses.
- **#128** SHA-pin GitHub Actions + BLOCKING `actions-pin-check` (merged) — provides the blocking-CI-guard pattern this slice copies for `openapi-drift-check`.
- **#135** Data-export library (in flight, PR #292) — coordination needed at pickup. If 135 is `merged` before 140's pickup, 140's engineer includes the export endpoints in the initial spec. If 135 is still in-flight, 140's engineer documents the existing surface only + a follow-on commit on slice 135's branch adds the export endpoints (slice 135's engineer owns the addition; slice 140's engineer documents the convention).

## Anti-criteria (P0 — block merge)

- **P0-A1: Every operation carries a `security` field.** No endpoint ships in the spec as `security: []` unless it is genuinely public (zero handler-side auth check). Drift-detect verifies the handler-side `requireAuth` / OPA gate maps to a documented `security` scheme.
- **P0-A2: Drift-detect is BLOCKING, not informational.** Added to `required_status_checks.contexts` in `.github/branch-protection.json`. Operator post-merge runs `scripts/apply-branch-protection.sh`. Cannot ship slice 140 without this — the only failure mode that makes the spec actively misleading is being out of sync.
- **P0-A3: Internal endpoints carry `x-internal: true` AND are filtered out of the public Redoc UI.** Operator-only / debug endpoints (`/health`, `/metrics`, `/v1/version`, `/v1/install-state`, etc.) MUST NOT render publicly.
- **P0-A4: Example values use neutral fixture tokens.** No vendor prefixes, no real emails, no real tenant names, no real bearer tokens. Same convention as slice 005 / 089 / 117.
- **P0-A5: Spec file size budget ≤ 500 KB.** Redoc bundle adds ≤ 1 MB to published site.
- **P0-A6: Auth-scheme tier matches handler OPA gate.** `bearer` (any authenticated) vs `adminBearer` (admin-only) vs narrower (`auditorBearer`, `grcEngineerBearer`). Drift-detect verifies the handler's OPA gate name maps to the documented `security` scheme.
- **P0-A7: NO gRPC endpoints in this spec.** The `.proto` files are the gRPC source of truth; OpenAPI is REST-only. No grpc-gateway translation layer (out of scope; v3 question).
- **P0-A8: NO auto-generated `.gen.yaml` sibling.** Single source of truth at `docs/openapi.yaml`.
- **P0-A9: NO bumping any existing handler's behavior to make the spec cleaner.** Spec describes reality; reality is not reshaped to match the spec.
- **P0-A10: NO removing endpoints from the public Redoc render without `x-internal: true` justification in the decisions log.**

## Skill mix (3-5)

- **`grill-with-docs`** — terminology + scope at pickup (verify "OpenAPI" not "Swagger" in code + docs; verify "spec" not "API doc" in user-facing copy).
- **Go OpenAPI generator** (D1 dependent) — `swaggo/swag` if (a), `kin-openapi` if (b), custom Go if (c).
- **Redoc / mkdocs Material integration** — D2 picks; the mkdocs page that embeds Redoc must theme cleanly.
- **CI drift-detect pattern** — slice 128's `actions-pin-check` is the closest template; slice 127's `branch-protection-drift` is the structural template.
- **`swagger-cli` or equivalent** — AC-8 validation tool.

## Notes for the implementing agent

**Recommended JUDGMENT call (D1 — generator):**

| Option                                                | Pros                                                                                                                             | Cons                                                                                                                                                                                       |
| ----------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| **(a) `swaggo/swag`** annotations                     | Most popular Go OpenAPI generator; chi-compatible; co-located source of truth (annotations next to handler); reasonable ceremony | Annotations clutter handler files; annotation syntax is fiddly; community is "established but not bustling"                                                                                |
| **(b) `kin-openapi` hand-authored YAML**              | Cleanest spec; full schema control; runtime validation library is good                                                           | Manual upkeep is the failure mode the drift-detect job exists to catch — but blocking drift-detect means every handler change requires a parallel hand edit to the YAML, which is friction |
| **(c) chi-route introspection + custom Go generator** | Zero annotations; precise on route table                                                                                         | Imprecise on request/response schemas (chi router doesn't carry that info); engineer writes the custom generator; new code surface to maintain                                             |

Maintainer lean: **(a) `swaggo/swag`**. Co-located is the right ergonomics for a fast-moving project. Annotations are visible in code review (a `// @Summary` comment changing IS the change). The drift-detect guard makes (a) safe even when annotations drift from behavior — the CI catches it before merge.

Pivot path: if `swaggo/swag` proves to have a blocking limitation discovered during implementation (e.g. doesn't handle a specific chi pattern the codebase uses heavily), fall back to (b) with a D1 entry explaining the pivot.

**Recommended JUDGMENT call (D2 — UI):**

Maintainer lean: **Redoc**. Reasons: (a) cleaner default theme that doesn't fight mkdocs Material; (b) static-render-friendly (the page bundles cleanly into the published site); (c) reads like documentation, not like an interactive sandbox (Swagger UI's "Try it out" affordance is a footgun for operators who might inadvertently fire requests against their own deployment).

If the engineer wants the interactive try-it affordance, that's a follow-on slice (mock-server gated; out of scope here).

**Recommended JUDGMENT call (D3 — drift-guard severity):**

Maintainer lean: **BLOCKING** — not informational. Justification:

- The spec being out of sync with handler reality is the only failure mode that makes the spec actively misleading.
- The cost of a 30-second CI re-run is far cheaper than an operator silently coding against a stale contract.
- Slice 128 set the precedent: discipline that must hold continuously gets a blocking guard. The spec is that kind of discipline.
- Operator post-merge ritual (`scripts/apply-branch-protection.sh`) is one extra step the maintainer accepts as part of shipping this slice.

If the engineer wants to ship the guard as informational at v1 and promote to blocking in a follow-on, that's acceptable IF the decisions log D3 entry explicitly documents the gate criteria for promotion (e.g. "3 weeks of green informational runs → promote").

**Coordination with slice 135 (data-export library):**

Slice 135 is in flight at PR #292 and adds a new HTTP family `/v1/admin/<entity>/export?format=...`. Two paths:

- **Path A (135 merges first):** slice 140's engineer documents the export endpoints in the initial spec along with everything else. Simpler.
- **Path B (140 merges first):** slice 135's engineer extends the spec as part of shipping each new export endpoint (the drift-detect guard would otherwise block their PR). slice 140's notes section here is the operator's signal that this convention applies.

Both paths are safe; the drift-detect guard makes the ordering robust.

**Spillover (Phase 7):**

Considered but not filed: **per-language SDK regen from spec**. The TypeScript SDK already exists (slice 003 area); regenerating it from the OpenAPI spec is a separate decision (which generator, which versioning strategy, which release cadence). Wait until slice 140 ships + the spec is stable enough to merit codegen; then file as slice 141+.

**Terminology (Phase 2 grill):**

- "OpenAPI" not "Swagger" in code + docs. "Swagger" is the legacy name (pre-OAS 3.0) + the name of the UI tool. The spec is OpenAPI 3.1.
- "Spec" not "API doc" / "schema" / "definition" in user-facing copy.
- "Drift-detect" not "lint" / "validate" for the CI guard (matches slice 127's `branch-protection-drift` + slice 128's `actions-pin-check` family).

**Already-built check (Phase 2 grill):**

- gRPC `.proto` files exist for connectors / evidence / admin-credentials / oscal; this slice does NOT touch them. The two API surfaces (gRPC + REST) stay separately specified.
- No existing OpenAPI infrastructure. No `swag init`. No `openapi.yaml`. This slice is greenfield for REST.

**Threat-model context (Phase 3 grill):**

The tampering risk is the load-bearing concern (a bad-actor PR weakening the spec to mislead operators). The BLOCKING drift-detect guard is the load-bearing mitigation. The information-disclosure risk (internal endpoints leaking into public UI) is the second-load-bearing concern; `x-internal: true` + Redoc filtering is the load-bearing mitigation.
