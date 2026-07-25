# Slice 500 — pgvector semantic-retrieval grounding (decisions log)

JUDGMENT slice. The subjective calls (embedding model, chunking strategy,
relevance/top-N threshold) are recorded here with rationale. The runtime
**AI-assist boundary is constitutional and untouched** — this log is a
development-process artifact, not a relaxation of that boundary.

**Status: gate-blocked at D0.** The two pre-work gates the slice defines were
checked first. Gate 1 (slice 498) passed. Gate 2 (the Postgres base image)
**failed and is escalated** — it is a deployment-wide call, not a code call
(slice boundary: "Do NOT change the project's Postgres base image without an
explicit decision"). No migration, retrieval code, or fixture was written on
this branch, because every one of them presupposes D0's answer: option (c)
below would not put the vectors in Postgres at all.

- detection_tier_actual: n/a (gate check, no implementation)
- detection_tier_target: integration

## Gate 1 — slice 498 landed ✅

Verified present on this branch's base (`origin/main`):

| Artifact                                      | Evidence                                                                                                                                             |
| --------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------- |
| `internal/llm` client seam                    | `internal/llm/client.go` — `Client` interface, one method `Generate(ctx, GenerateRequest) (GenerateResult, error)`                                   |
| Assembled-context seam                        | `GenerateRequest.Context map[string]any`, documented as "the substrate does NOT retrieve this (P0-498-2)" and "slice 500 assembles context upstream" |
| `ai_generations` audit row (+ context-inputs) | `migrations/sql/20260607000000_ai_generations.sql` and its `.down.sql`                                                                               |
| Local-default / cloud-opt-in substrate        | `internal/llm/ollama.go`, `internal/llm/cloud/`, `migrations/sql/20260612100000_tenant_llm_routing.sql`                                              |

**Note carried into the implementation fire.** Slice 500's spec (AC-2) assumes
it can call "the slice-498 substrate's embedding capability". That capability
does **not** exist: 498's decisions-log D1 explicitly rejected a multi-method
interface, recording that "embeddings belong to slice 500 (pgvector)". So this
slice must add the embedding seam itself. It should add a **separate `Embedder`
interface** rather than widening `Client` — widening `Client` would force the
stub and the slice-499 cloud impl to implement a method no generation surface
uses, which is the exact fat-interface outcome 498's D1 rejected.

## D0 — Postgres base image / pgvector provisioning: **OPEN, escalated**

**The finding (empirically verified, not assumed).** The project's Postgres
base image is `postgres:16-alpine` and it cannot load pgvector:

```
$ docker run --rm -e POSTGRES_PASSWORD=x -d postgres:16-alpine
$ psql -U postgres -c "CREATE EXTENSION IF NOT EXISTS vector;"
ERROR:  extension "vector" is not available
DETAIL:  Could not open extension control file
         "/usr/local/share/postgresql/extension/vector.control": No such file or directory.
HINT:  The extension must first be installed on the system where PostgreSQL is running.
```

`pg_available_extensions` in that image offers `pgcrypto` (1.3) and `pg_trgm`
(1.6) — both contrib — and no `vector`. pgvector is a third-party extension; it
is not in contrib, so no `postgres:*-alpine` tag will ever carry it.

**Blast radius — every environment, not just dev.** `postgres:16-alpine` is
pinned in:

- `deploy/docker/docker-compose.yml:59` (primary self-host store)
- `deploy/docker/docker-compose.edge.yml:99` (`postgres-edge`)
- `.github/workflows/ci.yml` — 5 service blocks (lines 301, 1447, 1706, 1934, 3693)

There is no existing extension build layer to extend: there is no custom
Postgres Dockerfile, no initdb-hook that builds extensions, and the only
`CREATE EXTENSION` anywhere in `migrations/sql/` is `pgcrypto` (contrib,
already present). Slice 268's decisions log independently corroborates this,
recording "no extension install (P0-A2)" and that pgvector "would need an
extension". Shared-cluster operators who do not run the compose `postgres`
service at all (documented at `docker-compose.yml:110`) are a third affected
population: for them this becomes a documented prerequisite on _their_ cluster,
whichever option is chosen.

**Why this is not the agent's call.** Changing the store image changes what
every self-host operator and every shared-cluster operator must run. It is a
deployment-wide, outward-facing decision with an upgrade path for existing
`pg-data` volumes attached to it.

### Options presented for decision

**(a) pgvector-flavoured base image** — swap to `pgvector/pgvector:pg16` (or
`ankane/pgvector`) in both compose files and the 5 CI service blocks.
_For:_ one-line-per-site change; the image is the upstream-maintained reference
build; `CREATE EXTENSION vector` then just works everywhere; CI and prod stay
identical, which is what keeps the RLS integration suite meaningful.
_Against:_ `pgvector/pgvector:pg16` is Debian-based, not Alpine — a larger
image and a different libc/base than the project standardised on; it is a
third-party publisher in the supply chain for the primary datastore; existing
`pg-data` volumes need verifying across the base change.

**(b) Extension build layer we own** — add `deploy/docker/postgres.Dockerfile`
that starts `FROM postgres:16-alpine` and compiles pgvector, publishing our own
tag.
_For:_ keeps the Alpine base and the current libc; we own the supply chain and
the pin; smallest deviation from the documented base.
_Against:_ we now maintain a Postgres image build (Alpine needs the build
toolchain added and stripped); CI service containers cannot build an image
inline, so the 5 CI blocks need a pre-published tag — meaning a new publish
workflow and registry artifact before any test can run; more moving parts than
(a) for the same end state.

**(c) External vector store** — keep `postgres:16-alpine` untouched, put
embeddings in a dedicated service.
_For:_ zero change to the primary datastore image and no migration risk.
_Against:_ this is Qdrant by another name, which **P0-500-3 explicitly forbids**
for this slice ("does NOT ship Qdrant — pgvector only; Qdrant is the v3
large-corpus follow-on"), and the locked tech-stack table pins pgvector for v2.
It also **forfeits the slice's primary security control**: AC-9 / threat-model
I make cross-tenant isolation _physical_ via four-policy RLS on
`app.current_tenant`, and the slice's own implementation note says do NOT
filter tenant in application code after an unscoped search. An external store
has no RLS, so tenant scoping degrades to exactly that application-code filter.
Recorded for completeness; not recommended.

**Recommendation: (a).** It reaches the required end state with the least new
machinery, and it keeps CI byte-identical to what operators run — which is the
property the cross-tenant-isolation proof (AC-9) depends on. (b) is the right
answer _if_ staying on Alpine/musl for the datastore is a hard constraint, and
its real cost is the publish-workflow prerequisite, not the Dockerfile. (c)
should be rejected: it trades away RLS-enforced tenant isolation and collides
with P0-500-3.

**Awaiting:** an explicit decision between (a), (b), and (c), plus a call on
the `pg-data` volume upgrade path for existing deployments.

## D1 — Embedding model: pending D0

Blocked on D0: option (c) would change the embedding-dimension constraints and
move the vectors out of Postgres entirely. Local Ollama stays the default path
and cloud embedding stays per-tenant opt-in via slice 499's routing banner
regardless of which option is chosen — that part is settled by
`P0-500-4` and is not in question here.

## D2 — Chunking strategy: pending D0

## D3 — Relevance / top-N threshold: pending D0

## Revisit once in use

- To be filled by the implementation fire once D0 is resolved.
