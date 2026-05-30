# CLAUDE.md — security-atlas

> Read this first when starting any session in this repo.

**Status:** v1 backlog fully merged on `main` (69/69 v1 slices; v2 follow-ons in progress). The system of record for design intent is still the canvas under `Plans/`; the system of record for implementation is `main` plus the merge trail in `docs/issues/_STATUS.md`.

---

## What this project is

`security-atlas` is an open-source, self-hostable, replacement-grade GRC platform — a control-graph and evidence-pipeline system that lets a security program run against many frameworks (SOC 2, ISO 27001, NIST CSF, PCI DSS, HIPAA, GDPR) from one source of truth. Spine: the [Secure Controls Framework](https://securecontrolsframework.com/) (~1,400 controls crosswalked to 200+ frameworks via NIST IR 8477 STRM). Wire format: NIST OSCAL.

**Primary user (v1):** the solo security leader at a 50–150-person security-product startup who runs the entire program — risk register, board reporting, SOC 2, vendor reviews, policies, exceptions — alone, and whose customers will diligence the diligence tool itself.

**v1 success test (binary):** does that user run their next SOC 2 audit out of security-atlas, generate the next board pack from it, and not reach for Vanta or a Google Sheet to fill a gap? If yes, v1 is done.

---

## Read the canvas (reading order)

The full design lives in `Plans/`. On a new session:

1. **Hub:** `Plans/ARCHITECTURE_CANVAS.md` — executive summary + three load-bearing decisions + navigation
2. **Canvas sections** in order:
   - `Plans/canvas/01-vision.md` — positioning, personas, non-goals, anti-patterns
   - `Plans/canvas/02-primitives.md` — Control, Risk, Evidence, Scope, Framework, Policy
   - `Plans/canvas/03-ucf.md` — Unified Control Framework graph
   - `Plans/canvas/04-evidence-engine.md` — Evidence SDK, connectors, control-as-code, questionnaires
   - `Plans/canvas/05-scopes.md` — multidimensional scope + **FrameworkScope intersection**
   - `Plans/canvas/06-risk.md` — risk register linkage
   - `Plans/canvas/07-metrics.md` — KPIs + **board reporting first-class**
   - `Plans/canvas/08-audit-workflow.md` — auditor role, OSCAL export, **audit-period freezing**
   - `Plans/canvas/09-tech-stack.md` — architectural commitments
   - `Plans/canvas/10-roadmap.md` — MVP through phase 3
   - `Plans/canvas/11-open-questions.md` — deferred decisions
3. **Companion deep-dives:**
   - `Plans/UCF_GRAPH_MODEL.md` — graph diagrams + worked example
   - `Plans/EVIDENCE_SDK.md` — full SDK contract including push profile
4. **Mockups:** `Plans/mockups/index.html` — iteration-1 UI mockups (HTML + Tailwind via CDN, no build)

For a specific design question, skip the linear read and jump to the relevant section.

---

## Constitutional principles (non-negotiable)

These are the design commitments that bound every decision. Do not propose features that violate them. If a request implies a violation, surface the conflict before acting.

### Architecture invariants

1. **One control, N framework satisfactions.** The UCF is a graph with STRM-typed edges through SCF anchors. Never duplicate controls per framework. (canvas §3, `UCF_GRAPH_MODEL.md`)
2. **Ingestion and evaluation are separated stages** with an append-only evidence ledger between them. Evaluation never writes to source-of-truth evidence. Bugs in evaluation never corrupt the record. Point-in-time replay is always possible. (canvas §4.3)
3. **The Evidence SDK exposes one canonical inbound API (`IngestEvidence`)** — a single `EvidenceIngestService.Push(record) → Receipt` gRPC RPC. Connectors are first-class peers in the operator's mental model: each is a separate process that holds source-side credentials and emits to the platform via `Push`. The connector's `profiles_supported` registration metadata (`pull`, `subscribe`, `push`) describes how the connector retrieves data **from the source** (scheduled poll, event subscription, or webhook receipt); the **platform-side wire surface is always push**. (canvas §4.1, `EVIDENCE_SDK.md`)
4. **Scope is multidimensional**, not a tree. Scope cells = tuples over (BU × env × geo × cloud × data_class × product). Controls have `applicability_expr`. (canvas §5.1–5.4)
5. **FrameworkScope intersects with control applicability.** PCI CDE ≠ HIPAA covered systems ≠ SOC 2 system. `effective_scope(control, framework) = applicability_expr ∩ framework_scope.predicate`. (canvas §5.5)
6. **Tenant isolation is enforced at the database layer** via PostgreSQL Row-Level Security on every tenant-scoped table. Not application code. RLS denies on missing context. (canvas §5.4)
7. **SCF is the canonical control catalog.** Mappings go requirement → SCF anchor, never requirement → requirement directly. (canvas §3.5)
8. **OSCAL is the wire format**, not the daily data model. Ingest catalogs/profiles/component-definitions; export SSP/AP/AR/POA&M. (canvas §3.4)
9. **Manual evidence is first-class.** Manual controls render the same UI surface as automated; lifecycle, ownership, freshness apply equally. (canvas §4.5)
10. **Audit-period freezing.** When an AuditPeriod is frozen, sample populations draw only from evidence with `observed_at ≤ frozen_at`. Live state continues independently. (canvas §8.4)

### Anti-patterns we explicitly reject

Do not propose or implement these. They are documented failures of existing GRC tools. (canvas §1.6, §1.2)

- Policy template libraries dressed as a feature (5 high-signal templates, not 50 placeholder docs)
- AI-generated policy text or audit responses without human approval — see boundary below
- Proprietary collector agents on endpoints (we use osquery / Fleet / read-only APIs)
- Vanity trust centers (defer until v3 unless customers actively demand)
- "Continuous monitoring" that's actually 24-hour polling (event-driven where APIs allow; name the interval honestly)
- Per-framework duplicated controls (violates invariant #1)
- Audit-period evidence pollution (violates audit-period freezing)
- Closed proprietary connectors (defeats the OSS thesis)

### AI-assist boundary (hard)

The platform supports AI assistance for:

- **Suggesting** draft questionnaire answers with **mandatory citations** to specific evidence IDs and/or policy IDs
- **Suggesting** SCF mappings for unmapped questionnaire questions (human approves once; mapping is canonical thereafter)
- **Summarizing** prior responses for similarity matching
- **Drafting** board-report narrative sections (templated, human-approved per section)
- **Explaining** gaps ("evidence covers SCF:IAC-06 but freshness is 95 days")

The platform does NOT:

- Publish any audit-binding artifact without one-click human approval
- Fabricate control coverage that has no evidence backing
- Auto-approve its own mappings
- Use Tenant A's confidential data to seed Tenant B's draft

Schema-level enforcement: `ai_assisted=true` records cannot have `human_approved=true` without `human_approver` set. Audit log shows model name + version + diff between AI draft and final. (canvas §4.6.5)

**Inference backend:** local Ollama is the default (no data leaves deployment). Cloud LLMs (Anthropic / OpenAI / Bedrock) are opt-in per-tenant with a visible banner indicating routing.

#### Board-narrative AI-assist (load-bearing — OQ #14 resolved 2026-05-20)

Board narratives are the highest-risk AI-assist surface because board members are typically non-technical and take outputs at face value. The hallucination cost is asymmetric vs. other AI-assist surfaces. Seven design decisions lock the shape when board-narrative v0 ships:

1. **Input shape = hybrid.** LLM sees a deterministic pre-computation rollup PLUS cited evidence excerpts for every claim. NOT raw evidence records (too expensive, model wanders). NOT pure rollup (compounds hallucination).
2. **Approval granularity = per-section.** Narrative split into numbered sections; each approve/edit/reject independently. NOT per-narrative (too coarse). NOT per-claim (too friction-heavy).
3. **Audit trail = full prompt + full response, every time.** System prompt + evidence inputs + generated draft + operator edits + final approved text. Forensically airtight; storage cost small (few KB per section).
4. **Mandatory citations** for every factual claim — validated to resolve to real evidence/control/risk IDs before the operator sees the draft; unresolved citation rejects the draft.
5. **Numeric claim verification** — every number ("94% fresh", "47 controls", "12 exceptions") auto-checked against the pre-computation; draft auto-rejected if numbers don't match ground truth.
6. **Section-shape enforcement** — LLM constrained to a numbered template; freestyle output rejected.
7. **Editor-mode operator UX** — operator edits inline; cannot approve text with unresolved citations.

**Tone discipline (banned phrases in the system prompt):** the LLM voice for board narratives is measured, factual, slightly defensive. Marketing-y / overly-positive framing is rejected. The non-exhaustive ban list:

- "we are proud to report"
- "exceeded expectations"
- "industry-leading"
- "best-in-class"
- "world-class"
- "robust" (when used as filler, not when describing a specific control posture)
- "leverage" (as a verb, when "use" works)
- any unprompted superlative

Full updated list maintained at slice 182's tone-anti-pattern reference document.

- Repetition discipline: vary recurring terminology in adjacent occurrences. The project's domain jargon ("load-bearing", "first-pass", "tracer-bullet", "diligence the diligence tool") is canonical, but flag if any single term appears 3+ times in one document — at that point the term is wearing thin and a synonym or a more specific phrasing usually reads cleaner.

**Project-specific exceptions:** the persona's Tier 2 list flags some words as AI-isms that this project uses literally. The canonical example is "harness" — slice 178 names the UI honesty audit (Playwright) harness, and downstream slices reference it by that name. Do not rewrite these literal references; the persona's list is supplementary, this project's list is canonical.

**Schema-level extensions when board-narrative v0 ships:** the `ai_assisted=true ↔ human_approver` invariant extends to require additional columns on board-narrative records: `prompt_version TEXT NOT NULL`, `model_name TEXT NOT NULL`, `model_version TEXT NOT NULL`, `model_provider TEXT NOT NULL` (e.g., `'ollama-local'` or `'anthropic-api'`). Old reports stay immutable — versioning is snapshot-at-generation, not retroactive.

**Default local model + refresh cadence:** Llama 3.1 8B Instruct as the default for board narratives at v0 (runs on 8-12GB GPU; commodity hardware). Quality caveat explicitly documented for operators. Cloud LLM opt-in per tenant for higher quality. **Default model recommendation refreshes every 6-12 months** as local models improve — the refresh is a documented maintenance task, not a slice (maintainer reviews + updates the operator docs).

**Implementation timing:** board-narrative v0 is v2+ work. Foundation pre-commitments land via slice 182.

**Forward note (banned-phrase enforcement):** the banned-phrase list above must be wired into the LLM system prompt when board-narrative v0 ships (slice 182's v2 continuation). No enforcement surface exists in v1 — the v1 board narrative is template-only (`internal/board/narrative.go`, a pure `text/template` renderer with no LLM call site). The list is documented here but not yet runtime-enforced; the v2 slice that introduces the call site owns wiring it in.

**This boundary governs the product at runtime — not the development process.** It is constitutional and unchanged. Separately, the _slice-development_ workflow has a `JUDGMENT` slice type (formerly `HITL`): when building a slice, Claude makes the subjective build-time calls itself (control-text accuracy, UX copy, rule-DSL shape, OSCAL conformance choices) and records them in a decisions log rather than blocking the merge on a human sign-off — the maintainer iterates post-deployment. That is a process choice about _how we build_. It does NOT touch this boundary, which is about _how the shipped product behaves_: the product still never publishes an audit-binding artifact without one-click human approval. Never conflate the two. See `Plans/prompts/04-per-slice-template.md` "Slice types".

### Licensing constraints (do not violate)

- **SCF**: free standard license — can be bundled. Legal review pending before ship (open question).
- **CCM (CSA)**: opt-in import only. Do not bundle CCM templates without a CSA commercial license.
- **CAIQ (CSA)**: ingest customer-supplied files only. Do not bundle CAIQ templates.
- **SIG (Shared Assessments)**: ingest customer-supplied files only. Members-only license precludes bundling.
- **HECVAT**: free — can be bundled.
- **OpenGRC code**: CC BY-NC-SA — do not copy code. Concepts and patterns may inform our own implementation.
- **security-atlas's own license**: open decision (Apache 2.0 vs AGPL).

---

## Tech stack (locked-in)

| Layer                       | Choice                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                  | Notes                                                                                                                                                                                                    |
| --------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Backend language**        | Go (platform core)                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                      | Static binary, low operational overhead, strong concurrency for evidence streams                                                                                                                         |
| **Secondary language**      | Python (connector SDK reference + OSCAL bridge via compliance-trestle)                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                  | Bridged from Go via stable gRPC contract                                                                                                                                                                 |
| **Database**                | PostgreSQL 16+                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                          | RLS for tenancy, JSONB for evolving evidence, recursive CTEs for UCF graph traversal                                                                                                                     |
| **DB access**               | **sqlc + Atlas** (sqlc pinned to `v1.31.1` in `justfile` — slice 109)                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                   | Type-safe Go from SQL; declarative migrations. Recursive CTEs and JSONB are first-class. No ORM impedance mismatch.                                                                                      |
| **Object storage**          | S3-compatible                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                           | For evidence artifacts > 1 MB                                                                                                                                                                            |
| **Analytics (optional v2)** | ClickHouse                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                              | Behind a read-model interface. Only when evidence-record volume crosses ~10⁹.                                                                                                                            |
| **Event/queue**             | NATS JetStream                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                          | Single binary; durable streams; replay for evidence reprocessing                                                                                                                                         |
| **IPC**                     | gRPC                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                    | Connector SDK contract + Python OSCAL bridge                                                                                                                                                             |
| **Push API**                | REST `POST /v1/evidence:push` + gRPC streaming + CLI + per-language SDKs (Go, Python, TypeScript, Java)                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                 | See `Plans/EVIDENCE_SDK.md`                                                                                                                                                                              |
| **Auth (AuthN)**            | OIDC (relying party only — we are not an IdP). Atlas-AS-as-OIDC-RP authenticates the human via external IdP; atlas-AS-as-issuer mints the atlas JWT.                                                                                                                                                                                                                                                                                                                                                                                                                                                    | Every credible IdP speaks it                                                                                                                                                                             |
| **Authorization Server**    | Internal OAuth 2.0 AS layer issuing JWT access tokens (ES256 per slice 187 decision D1) per RFC 9068 JWT Profile · RFC 8693 Token Exchange (tenant-switch verb) · RFC 7636 PKCE (browser) · RFC 8628 Device Authorization Grant (CLI) · RFC 7009 Revocation · RFC 7662 Introspection. JWT claims carry `atlas:current_tenant_id`, `atlas:available_tenants[]`, `atlas:roles{tenant→[role]}`, `atlas:super_admin`. Foundation lives in `internal/auth/keystore` + `internal/auth/jwt` + `internal/auth/tokensign` + `internal/api/oauth`; JWS library is `github.com/go-jose/go-jose/v4` (slice 187 D2). | Resolves OQ #21 (Reading D, 2026-05-20). Spine: slices 187-192. [ADR-0003](docs/adr/0003-oauth-authorization-server.md). Composes with the OIDC RP — RP authenticates the human; AS mints the atlas JWT. |
| **Auth (AuthZ)**            | RBAC (coarse roles) + ABAC (fine cuts) via OPA                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                          | Same OPA engine evaluates control queries and authorization decisions                                                                                                                                    |
| **OPA deployment**          | Embedded Go library (v1)                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                | Sidecar / central server is a v2 option                                                                                                                                                                  |
| **Frontend**                | **Next.js 16 App Router + shadcn/ui + Tailwind 4 + TanStack Query** (verified 2026-05-15: `next@16.2.6`, `react@19.2.6`, `eslint@^9` per slice 078)                                                                                                                                                                                                                                                                                                                                                                                                                                                     | Server Components for data-heavy dashboards; shadcn/ui aligns with mockups                                                                                                                               |
| **Schema registry**         | In-tree Go service (v1), backed by Postgres                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                             | Apicurio / external is a v3 option                                                                                                                                                                       |
| **Vector store**            | pgvector (v2 when AI-assist lands)                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                      | Qdrant is a v3 option for large corpora                                                                                                                                                                  |
| **AI inference**            | Local Ollama default (`llama3.1:8b-instruct-q5` baseline)                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                               | Cloud LLM opt-in per-tenant                                                                                                                                                                              |
| **Evidence integrity**      | sha256 content-hash per record (v1) + cosign signing of audit-export bundles                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                            | Full Sigstore transparency-log in v3                                                                                                                                                                     |
| **Observability**           | OTEL native (traces + metrics + logs); default docker-compose bundles Prometheus + Grafana + Tempo + Loki                                                                                                                                                                                                                                                                                                                                                                                                                                                                                               | Production users route OTEL to their own stack                                                                                                                                                           |
| **Build runner**            | `just` (justfile at root)                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                               | Cross-language; cleaner than Make                                                                                                                                                                        |
| **Go tooling**              | Go modules · `gofmt` · `goimports` · `golangci-lint` (strict)                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                           | Enforced via pre-commit + CI                                                                                                                                                                             |
| **Python tooling**          | `uv` (env + deps) · `ruff` (format + lint)                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                              | Modern Python toolchain                                                                                                                                                                                  |
| **TS tooling**              | `npm` workspaces · `prettier` · `eslint` · `tsc --strict`                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                               |                                                                                                                                                                                                          |
| **CI/CD**                   | GitHub Actions                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                          | Free for OSS; OIDC token issuance for push credentials                                                                                                                                                   |
| **Container**               | Distroless base images; multi-stage builds                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                              |                                                                                                                                                                                                          |
| **Deployment**              | docker-compose (self-host solo) · Helm chart (K8s SaaS)                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                 | Single VM is the target for v1 self-host                                                                                                                                                                 |
| **Repo shape**              | **Monorepo** (single repo, all components, all languages)                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                               | Cross-cutting changes are one PR                                                                                                                                                                         |
| **Mockup framework**        | Plain HTML + Tailwind via CDN                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                           | Iteration-1 only. Graduate to shadcn/ui React when frontend code begins.                                                                                                                                 |

---

## Planned repository layout

When code starts (do not scaffold without explicit user approval):

```
security-atlas/
├── CLAUDE.md                     # this file
├── README.md                     # public-facing (write when code begins)
├── LICENSE                       # decide: Apache 2.0 vs AGPL (open question)
├── justfile                      # cross-language task runner
├── go.work                       # Go workspace (multi-module monorepo)
├── package.json                  # npm workspace root (frontend + TS SDK)
├── pyproject.toml                # uv root for Python
│
├── Plans/                        # planning docs (system of record pre-code)
│   ├── ARCHITECTURE_CANVAS.md   # hub
│   ├── canvas/                   # split sections (01-11 + sources)
│   ├── UCF_GRAPH_MODEL.md       # graph deep dive
│   ├── EVIDENCE_SDK.md          # SDK deep dive
│   └── mockups/                  # iteration-1 HTML mockups
│
├── cmd/                          # Go main entrypoints
│   ├── atlas/                    # platform binary
│   ├── atlas-cli/                # CLI (`security-atlas evidence push`, etc.)
│   └── atlas-oscal/              # OSCAL bridge (talks to Python via gRPC)
│
├── internal/                     # private Go packages
│   ├── catalog/                  # SCF + framework versioning
│   ├── evidence/                 # ledger + ingestion stage
│   ├── eval/                     # evaluation stage
│   ├── ucf/                      # graph queries
│   ├── scope/                    # scope + framework-scope
│   ├── risk/, policy/, audit/, board/
│   ├── auth/                     # OIDC RP + RBAC + ABAC via OPA
│   ├── tenancy/                  # RLS context plumbing
│   └── api/                      # HTTP + gRPC handlers
│
├── pkg/                          # public Go packages
│   └── sdk-go/                   # Go push SDK
│
├── connectors/                   # per-connector implementations (any language)
│   ├── aws/, github/, okta/, gcp/, azure/, k8s/
│   ├── osquery/, jamf/, intune/
│   ├── jira/, linear/, slack/
│   ├── 1password/, bitwarden/
│   ├── datadog/, pagerduty/, grafana/
│   ├── rippling/, bamboohr/, workday/
│   └── manual/                   # CSV / S3 / SFTP / upload
│
├── sdk/                          # non-Go SDKs
│   ├── python/                   # pyatlas
│   ├── typescript/               # @security-atlas/sdk
│   └── java/                     # v2
│
├── web/                          # Next.js 15 App Router frontend
│   ├── app/                      # routes
│   ├── components/               # shadcn/ui + custom
│   ├── lib/                      # client utilities
│   └── ...
│
├── oscal-bridge/                 # Python service wrapping compliance-trestle
│
├── proto/                        # gRPC protobuf definitions
│
├── schemas/                      # JSON Schemas for evidence_kind
│   ├── sast.scan_result.v1.json
│   ├── access_review.completion.v1.json
│   └── ...
│
├── migrations/                   # Atlas declarative migrations
│   └── schema.hcl
│
├── policies/                     # OPA Rego — both control policies and authz
│
├── deploy/
│   ├── docker/                   # Dockerfiles + docker-compose
│   └── helm/                     # Helm chart
│
├── .github/
│   └── workflows/                # CI: build, test, lint, release
│
└── docs/                         # generated docs site (mkdocs-material or Docusaurus — open)
```

---

## Working norms in this repo

### Editing `Plans/` vs editing code

1. **Canvas (`Plans/canvas/*.md`) edits** — write to the split files, not the hub. The hub (`Plans/ARCHITECTURE_CANVAS.md`) is an index — only edit it for executive summary / navigation / load-bearing-decisions changes.
2. **Companion docs** (`UCF_GRAPH_MODEL.md`, `EVIDENCE_SDK.md`) stay at `Plans/` root, not under `canvas/`.
3. **Mockups in `Plans/mockups/`** were iteration-1 HTML; the production frontend now lives at `web/`. Treat the mockups as reference, not production code.
4. **New architectural decisions** land as ADRs under `docs/adr/NNNN-*.md` (per the documentation discipline); the canvas captures the resolved invariant, the ADR captures the trade-off context.

### Spine ordering (already executed; left as the historical record)

The v1 spine was built in this order — preserved here so future contributors understand the dependency shape:

1. Bootstrap the monorepo skeleton (slice 001 — `justfile` + `go.work` + `package.json` + `pyproject.toml` + empty directory structure + CI workflows).
2. Schema + migrations for the six primitives (Control, Risk, Evidence, Scope, Framework, Policy) + FrameworkScope before any feature work (slice 002).
3. Evidence SDK contract — proto definitions + Go push client + CLI (slice 003) — before any connector.
4. First connector: AWS, deepest demand, well-documented APIs (slice 004).
5. Frontend bootstrap (slice 005) after the platform had a real API to talk to.

### Style

- **No emojis** in code, docs, or commit messages unless the user explicitly requests them.
- **Markdown over prose:** prefer tables, lists, and short paragraphs over walls of text.
- **Cite sources** when making factual claims (versions, license terms, vendor behavior). Sources live in `Plans/canvas/sources.md`.
- **Conventional Commits** when code commits begin (`feat:`, `fix:`, `docs:`, `chore:`, etc.).
- **Co-authored-by** trailer on AI-assisted commits.

### Branching

- `main` is the only long-lived branch.
- Feature branches: `<area>/<short-description>` (e.g., `evidence/sdk-push-protocol`, `ucf/scf-importer`).
- Squash-merge to main with rewritten Conventional Commit messages.

### Asking for help vs. acting

- **Read first.** If a design question lives in the canvas, surface what the canvas says before proposing alternatives.
- **Ask before scaffolding.** Repo structure, new dependencies, new top-level dirs — confirm before creating.
- **Ask before destructive operations.** Deleting files, rewriting commits, force-pushing. Especially in `Plans/`.
- **Don't invent.** If a tech-stack choice isn't here and isn't in `Plans/canvas/09-tech-stack.md`, ask.

---

## Testing discipline (four enforced surfaces)

Slice 069 ratchets the project's verification surfaces from "tests exist" to "tests gate merges". Every PR resolves four named checks before branch-protection unlocks the merge button.

| Surface             | Entry point                                                                             | What it covers                                                                          | Floor / gate                                                                                     |
| ------------------- | --------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------ |
| Go unit             | `go test ./...` (CI: `Go · build + test`)                                               | Per-package Go logic                                                                    | Per-package floor in `cmd/scripts/coverage-thresholds.json`; gate is `cmd/scripts/coverage-gate` |
| Go integration      | `go test -tags=integration -p 1 ./internal/...` (CI: `Go · integration (Postgres RLS)`) | RLS, migrations, Postgres-backed handlers, real services (NATS + MinIO)                 | No coverage gate yet; presence-of-tests is the gate (CI fails on test failure)                   |
| Frontend vitest     | `npm run test` from `web/` (CI: `Frontend · vitest`)                                    | Module-level web logic: BFF route handlers, `lib/api.ts`, `lib/api/bff.ts`              | No coverage gate yet; CI uploads `coverage-summary.json` as an artifact to inform follow-up      |
| Frontend Playwright | `npm run test:e2e` from `web/` (CI: `Frontend · Playwright e2e`)                        | User flows: dashboard, control detail, audit workspace, risk hierarchy, admin bootstrap | CI fails on spec failure; failed runs upload HTML report + traces as artifacts                   |

**Where each lives:**

- Go unit tests: `internal/<pkg>/*_test.go` (no build tags)
- Go integration tests: `internal/<pkg>/*_test.go` with `//go:build integration`
- Frontend vitest: `web/lib/**/*.test.ts`, `web/app/api/**/*.test.ts` (vitest config: `web/vitest.config.ts`)
- Frontend Playwright: `web/e2e/*.spec.ts` (Playwright config: `web/playwright.config.ts`; runner docs: `web/e2e/README.md`)
- Vitest-vs-Playwright decision matrix: `web/testing.md`

**Raising a coverage floor (Go):** write the missing tests in the SAME PR as the floor lift. Do NOT lift a threshold without writing the tests; do NOT write tests in a PR that does not lift the threshold (the gate is a ratchet — it must monotonically increase).

**Adding a new e2e spec:** see `web/e2e/README.md`. Spec preconditions (seed data, test bearers) must be establishable by the docker-compose bring-up; if a spec needs preconditions the bootstrap cannot provide, file a spillover slice for the seed harness rather than relaxing the spec.

### Test-tier conventions

Slice 353 formalizes four conventions surfaced by slice 333's QA strategy audit (`docs/audits/333-qa-strategy-gap-analysis.md`). These are conventions, not new gates — they document patterns already practiced so they stop being rediscovered per-package. Cross-references slice 334 (`docs/audits/334-test-framework-review.md`) where the framework-level finding overlaps.

- **Pure-Go pre-DB unit convention (Q-2).** The canonical way to lift a package's coverage floor is to add a `helpers_test.go` alongside the integration suite that exercises the package's pure-Go branches (validators, normalizers, formatters, predicate guards, pre-transaction input checks) with fast `t.Parallel()` table tests — no Postgres, no build tag. This is the slice 290 / 297 / 310 / 313 / 315 / 318 / 320 pattern. When raising a floor, prefer adding pure-Go unit branches first (fast feedback, no `-p 1` serialization cost) and only reach for additional integration assertions when the branch genuinely requires real services. The integration tier stays the safety net; the pure-Go base is the fast loop.

- **Component-test surface (Q-3 — decided OUT of scope).** `web/vitest.config.ts` pins `environment: "node"` (slice 069 P0-A3): vitest is a node-only module-logic tier (BFF route handlers, `lib/api.ts`, `lib/api/bff.ts`) — no JSX, no DOM. React component-level tests (React Testing Library + a `happy-dom` vitest project) are **explicitly OUT of scope for v1**. The Playwright e2e tier is the de facto component-test tier: a misnamed testid, a missing ARIA attribute, or a `<Button>` variant regression is caught there. The cost (a component regression costs an e2e spec's ~3-5s rather than a ~50ms unit) is accepted for v1. Revisit if/when component-level churn makes the e2e tier's wall-clock the bottleneck; that revisit is a slice, not a drift.

- **Integration enrolment policy (Q-7).** Every Go package that imports `internal/db/dbx` or sets `app.current_tenant` **SHOULD** ship an `integration_test.go` (`//go:build integration`) and enrol it in the integration job's package list. The enforcement mechanism is the slice 345 discovery primitive (`scripts/audit-integration-enrolment.sh` + the `integration-enrolment-check` CI job), which fails when a tagged package is neither enrolled nor on its `KNOWN_UNENROLLED` ratchet. The policy is "ship the integration test by default; the discovery primitive catches the package that forgets." Draining the `KNOWN_UNENROLLED` backlog is slice 390's job. See also `CONTRIBUTING.md` "Integration-test enrolment".

- **Integration tier retry policy (Q-16 — decided: no retry, investigate every flake).** The Go integration tier (`go test -tags=integration -p 1 ./internal/...`) has **no `-retry`**: a flake there is a hard fail and is investigated to root cause (the slice 340 / 341 chromedp pattern). This is deliberate and asymmetric with the Playwright tier (`retries: isCI ? 1 : 0` in `web/playwright.config.ts`): real-services races (NATS startup, Postgres `pg_isready`, MinIO bucket-create) are the kind of flake whose root cause is usually a real bring-up ordering bug worth fixing once, not papering over with a retry. The flake-budget dashboard (slice 352) tracks the aggregate rate so the "investigate now?" call is mechanical, not per-incident judgment.

### Flake budget

Slice 352 formalizes the per-surface flake budget proposed in slice 333's QA audit. The aggregate-rate signal lives at [`docs/flake-budget.md`](docs/flake-budget.md); the weekly-refreshed dashboard at [`docs/flake-budget-dashboard.md`](docs/flake-budget-dashboard.md) is generated by [`scripts/flake-counter.sh`](scripts/flake-counter.sh) via [`.github/workflows/flake-counter.yml`](.github/workflows/flake-counter.yml). The budget does **not** relax the merge-block bar — every flake still blocks the merge it occurs on; the budget tracks the aggregate rate so that "is this worth investigating?" becomes a mechanical decision (trigger threshold crossed = `flake-investigation` issue filed automatically) rather than per-incident judgment. Updates to the budget table itself require a slice; the dashboard is a derived artifact.

### Defect detection-tier classification

Slice 353 (Q-13 from slice 333's audit) adds two fields to every JUDGMENT-slice decisions log: `detection_tier_actual` (where a bug found during the slice WAS caught) and `detection_tier_target` (where it SHOULD have been caught). Allowed values: `unit`, `integration`, `playwright`, `contract`, `manual_review`, `production`, `none` (no bug surfaced during the slice). The template lives in [`Plans/prompts/04-per-slice-template.md`](Plans/prompts/04-per-slice-template.md) ("Detection-tier classification"). Aggregated over time, a recurring `target=unit, actual=production` pattern is a coverage-tier gap; a recurring `target=integration, actual=fix_forward` pattern is an integration-enrolment gap (Q-7). The cost is one line per decisions log; the payoff is an aggregate signal the project lacks today (slice 333 Theme 3). The companion fix-forward-rate metric (Q-14) is tracked in [`docs/fix-forward-rate.md`](docs/fix-forward-rate.md).

---

## Open decisions remaining (track and resolve before relevant code lands)

These are explicitly deferred. Do not pick one unilaterally. (Full list: `Plans/canvas/11-open-questions.md`.)

| Decision                                                                   | Decide before…                                                                                                                                                                                                                                           |
| -------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Project license (Apache 2.0 vs AGPL)                                       | First public code push beyond Plans/                                                                                                                                                                                                                     |
| SCF redistribution terms (legal review)                                    | Bundling SCF catalog in releases                                                                                                                                                                                                                         |
| Hosted offering or pure OSS governance                                     | Public launch                                                                                                                                                                                                                                            |
| FrameworkScope ownership workflow UX                                       | PCI/HIPAA modules ship                                                                                                                                                                                                                                   |
| Push credential issuance UX (API key rotation, scoping, revocation)        | Push CLI ships                                                                                                                                                                                                                                           |
| Schema-registry governance for community `evidence_kind`s                  | Community connectors land                                                                                                                                                                                                                                |
| AI-assistance boundary in contributor docs                                 | First AI feature lands                                                                                                                                                                                                                                   |
| Risk-methodology default lock (NIST 800-30 + 5x5 + dollar-banded vs FAIR)  | Risk module ships                                                                                                                                                                                                                                        |
| Privacy module shape (sibling vs first-class)                              | GDPR support work begins                                                                                                                                                                                                                                 |
| Disclosure / breach-notification workflow shape                            | HIPAA / GDPR Art. 33 work                                                                                                                                                                                                                                |
| CCM / FedRAMP elevation timing                                             | When user demand surfaces                                                                                                                                                                                                                                |
| Control catalog governance (community-contributed controls, verified tier) | Public marketplace conversation                                                                                                                                                                                                                          |
| ~~Docs site generator (mkdocs Material vs Docusaurus)~~                    | RESOLVED 2026-05-14 (slice 058): **mkdocs Material**. See [`Plans/canvas/11-open-questions.md`](Plans/canvas/11-open-questions.md) item 20 + [`docs/audit-log/058-user-docs-scaffold-decisions.md`](docs/audit-log/058-user-docs-scaffold-decisions.md). |

---

## Quick references

- Repo on GitHub: https://github.com/mgoodric/security-atlas (private)
- Canvas hub: `Plans/ARCHITECTURE_CANVAS.md`
- Mockups: open `Plans/mockups/index.html` in a browser

---

**When in doubt:** read the canvas section relevant to the question, then ask before guessing. The design is opinionated for a reason — most ambiguity is resolved in `Plans/`, not invented at the keyboard.
