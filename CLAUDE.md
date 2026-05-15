# CLAUDE.md — security-atlas

> Read this first when starting any session in this repo.

**Status:** Pre-implementation ideation. No application code yet. The system of record is the canvas under `Plans/`.

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
3. **The Evidence SDK exposes one canonical inbound API (`IngestEvidence`)** through two profiles: **connector** (pull/subscribe) and **pusher** (push). Both are first-class peers. (canvas §4.1, `EVIDENCE_SDK.md`)
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

| Layer                       | Choice                                                                                                    | Notes                                                                                                               |
| --------------------------- | --------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------- |
| **Backend language**        | Go (platform core)                                                                                        | Static binary, low operational overhead, strong concurrency for evidence streams                                    |
| **Secondary language**      | Python (connector SDK reference + OSCAL bridge via compliance-trestle)                                    | Bridged from Go via stable gRPC contract                                                                            |
| **Database**                | PostgreSQL 16+                                                                                            | RLS for tenancy, JSONB for evolving evidence, recursive CTEs for UCF graph traversal                                |
| **DB access**               | **sqlc + Atlas**                                                                                          | Type-safe Go from SQL; declarative migrations. Recursive CTEs and JSONB are first-class. No ORM impedance mismatch. |
| **Object storage**          | S3-compatible                                                                                             | For evidence artifacts > 1 MB                                                                                       |
| **Analytics (optional v2)** | ClickHouse                                                                                                | Behind a read-model interface. Only when evidence-record volume crosses ~10⁹.                                       |
| **Event/queue**             | NATS JetStream                                                                                            | Single binary; durable streams; replay for evidence reprocessing                                                    |
| **IPC**                     | gRPC                                                                                                      | Connector SDK contract + Python OSCAL bridge                                                                        |
| **Push API**                | REST `POST /v1/evidence:push` + gRPC streaming + CLI + per-language SDKs (Go, Python, TypeScript, Java)   | See `Plans/EVIDENCE_SDK.md`                                                                                         |
| **Auth (AuthN)**            | OIDC (relying party only — we are not an IdP)                                                             | Every credible IdP speaks it                                                                                        |
| **Auth (AuthZ)**            | RBAC (coarse roles) + ABAC (fine cuts) via OPA                                                            | Same OPA engine evaluates control queries and authorization decisions                                               |
| **OPA deployment**          | Embedded Go library (v1)                                                                                  | Sidecar / central server is a v2 option                                                                             |
| **Frontend**                | **Next.js 15 App Router + shadcn/ui + Tailwind 4 + TanStack Query**                                       | Server Components for data-heavy dashboards; shadcn/ui aligns with mockups                                          |
| **Schema registry**         | In-tree Go service (v1), backed by Postgres                                                               | Apicurio / external is a v3 option                                                                                  |
| **Vector store**            | pgvector (v2 when AI-assist lands)                                                                        | Qdrant is a v3 option for large corpora                                                                             |
| **AI inference**            | Local Ollama default (`llama3.1:8b-instruct-q5` baseline)                                                 | Cloud LLM opt-in per-tenant                                                                                         |
| **Evidence integrity**      | sha256 content-hash per record (v1) + cosign signing of audit-export bundles                              | Full Sigstore transparency-log in v3                                                                                |
| **Observability**           | OTEL native (traces + metrics + logs); default docker-compose bundles Prometheus + Grafana + Tempo + Loki | Production users route OTEL to their own stack                                                                      |
| **Build runner**            | `just` (justfile at root)                                                                                 | Cross-language; cleaner than Make                                                                                   |
| **Go tooling**              | Go modules · `gofmt` · `goimports` · `golangci-lint` (strict)                                             | Enforced via pre-commit + CI                                                                                        |
| **Python tooling**          | `uv` (env + deps) · `ruff` (format + lint)                                                                | Modern Python toolchain                                                                                             |
| **TS tooling**              | `npm` workspaces · `prettier` · `eslint` · `tsc --strict`                                                 |                                                                                                                     |
| **CI/CD**                   | GitHub Actions                                                                                            | Free for OSS; OIDC token issuance for push credentials                                                              |
| **Container**               | Distroless base images; multi-stage builds                                                                |                                                                                                                     |
| **Deployment**              | docker-compose (self-host solo) · Helm chart (K8s SaaS)                                                   | Single VM is the target for v1 self-host                                                                            |
| **Repo shape**              | **Monorepo** (single repo, all components, all languages)                                                 | Cross-cutting changes are one PR                                                                                    |
| **Mockup framework**        | Plain HTML + Tailwind via CDN                                                                             | Iteration-1 only. Graduate to shadcn/ui React when frontend code begins.                                            |

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

### Pre-implementation phase (now)

1. **Default to editing `Plans/`.** Most changes are markdown.
2. **Write to the canvas split files**, not the hub. The hub (`Plans/ARCHITECTURE_CANVAS.md`) is an index — only edit it for executive summary / navigation / load-bearing-decisions changes.
3. **Companion docs** (`UCF_GRAPH_MODEL.md`, `EVIDENCE_SDK.md`) stay at `Plans/` root, not under `canvas/`.
4. **Mockups in `Plans/mockups/`** are iteration-1 HTML. Don't refactor them into a build system yet. When real frontend code begins, the mockups become reference, not production code.
5. **No code commits without explicit user approval to start scaffolding.** Even small ones. Ask first.

### When code begins

1. Bootstrap the monorepo skeleton in one PR (justfile + go.work + package.json + pyproject.toml + empty directory structure + CI workflows).
2. Land migrations and the schema for the six primitives (Control, Risk, Evidence, Scope, Framework, Policy) + FrameworkScope before any feature work.
3. Build the Evidence SDK contract (proto definitions + Go push client + CLI) before any connector.
4. First connector: AWS (deepest demand, well-documented APIs).
5. Frontend bootstraps after the platform has a real API to talk to.

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
