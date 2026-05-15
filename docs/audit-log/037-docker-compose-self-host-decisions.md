# 037 — docker-compose self-host bundle — decisions log

Slice 037 is `Type: AFK` in its frontmatter, but the scope call below
(expanding into a small in-scope Go touch) is a genuine build-time
judgment. This log records it in the JUDGMENT-slice format so the
maintainer can re-evaluate the calls once the bundle is in real use.

## Decisions made

### 1. Scope: a ~50-line in-scope Go touch, not deploy-only

**Options considered:**

- **(A) Deploy-only.** Ship `deploy/docker/**` + justfile recipes, mark
  AC-2 / AC-3 / AC-4 / AC-5 as "blocked — needs a follow-up slice."
- **(B) Minimal in-scope Go touch.** Add the small amount of platform
  code the slice's own ACs require.

**Chosen: (B).**

**Rationale.** `_STATUS.md` carried a planning-time estimate scoping 037
as `deploy/docker/** + justfile · no migrations`. Codebase verification
during the grill found that estimate was written before anyone checked
the platform code:

- AC-2 (`GET /health` → 200): no `/health` route handler existed.
  `internal/api/httpserver.go` only declared `/health` as an
  authz-_exempt prefix_ — a request would fall through to a 404.
- AC-3 (web UI sign-in, local mode): `cmd/atlas/main.go` never called
  `AttachAuthHandler`, so `/auth/local/login` never mounted.
- AC-4 / AC-5 (50 controls seeded; first evidence): control-bundle
  upload needs a running server + an auth token; the existing
  `ATLAS_BOOTSTRAP_TENANT` path issues a _random_ token printed to
  stderr — an offline one-shot bootstrap container cannot consume it.

A self-host bundle whose own headline acceptance criterion (`/health`
200, sign-in works, controls seeded) cannot pass is not a shippable
bundle. The slice's ACs are the real boundary; the `_STATUS.md` line is
an estimate, not a constitutional invariant. The Go touch is genuinely
minimal and has zero overlap with the parallel batch-21 slice (012,
`internal/eval/*`). The `internal/api/httpserver.go` overlap with 012 is
the known-safe chi mount-append 3-way merge.

**The Go touch, enumerated:**

| File                                  | Change                                                    |
| ------------------------------------- | --------------------------------------------------------- |
| `internal/api/httpserver.go`          | `root.Get("/health", s.handleHealth)` + handler (+exempt) |
| `cmd/atlas/main.go`                   | `AttachAuthHandler` wiring block (local mode, no IdP)     |
| `cmd/atlas/main.go`                   | `ATLAS_BOOTSTRAP_TOKEN` → fixed-token admin credential    |
| `internal/api/server.go`              | `IssueBootstrapFixedAdminCredential` passthrough          |
| `internal/api/credstore/credstore.go` | `IssueFixedAdmin` — fixed-token admin credential          |
| `cmd/atlas-cli/cmd_bootstrap.go`      | `atlas-cli bootstrap hash-password` (format-correct hash) |
| `cmd/atlas-cli/root.go`               | register the bootstrap command                            |
| `web/next.config.ts`                  | `output: "standalone"` for a slim runtime image           |

It came in slightly above the "~25 line" pre-estimate (~50 lines across
the platform files) because two additional small seams were needed for
the bundle to actually seed itself unattended: a deterministic bootstrap
token, and a format-guaranteed password hasher. Both are squarely "make
the slice's ACs pass," not new product surface. **Confidence: high.**

### 2. `/health` is liveness, not readiness — always 200

`handleHealth` returns HTTP 200 whenever the process is serving HTTP. If
a DB pool is attached it runs a 2-second `Ping`; a failed ping reports
`{"db":"degraded"}` but the status code stays 200.

**Rationale.** `/health` is wired as the compose healthcheck-adjacent
liveness signal and the `atlas-bootstrap` readiness poll. Returning 503
on a transient DB blip would make compose mark `atlas` unhealthy and
restart-loop it during Postgres warm-up. Bootstrap ordering already gates
`atlas` on `postgres: service_healthy` and on the bootstrap one-shot
completing, so the DB is reachable by the time `atlas` runs. A separate
`/readyz` (true readiness, 503 when not ready) can land later if a
load-balancer needs it. **Confidence: high.**

### 3. CI validates `docker compose config`, not a full smoke-up

CI (and the `just self-host-config` recipe) validate that
`docker-compose.yml` parses. CI does **not** bring the full stack up.

**Rationale.** The slice instructions explicitly permit this floor, and
the slice-036 retro flagged GitHub Actions service-container pain
(`minio/minio` needs an explicit `server /data`; `bitnami/minio` is
unpullable; NATS needs explicit `-js -sd`). A full bring-up in CI would
be slow and flaky for marginal signal. `docs/getting-started/first-evidence.md`
and `docs/SELF_HOSTING.md` both document the manual smoke test instead.
**Confidence: medium** — revisit if the bundle regresses silently more
than once; a nightly (non-PR-blocking) smoke-up job would be the fix.

### 4. Dockerfile naming: `atlas.Dockerfile` / `atlas-cli.Dockerfile` / `web.Dockerfile`

The slice doc suggested `Dockerfile.atlas` / `Dockerfile.web`. The
existing `.github/workflows/container-publish.yml` already references
`deploy/docker/atlas.Dockerfile` and `deploy/docker/atlas-cli.Dockerfile`
— files that did not exist yet. 037 creates them under the names the
release pipeline already expects, and adds `web.Dockerfile` for
consistency. Matching the workflow keeps `container-publish.yml` working
without an edit. **Confidence: high.**

### 5. `atlas-bootstrap` is a one-shot compose service, not an image entrypoint hack

First-boot logic (migrations + seed + SCF import + control upload) lives
in a dedicated short-lived container (`bootstrap.Dockerfile`, built on
`postgres:16-alpine` for `psql` + a shell, with the static `atlas-cli`
binary copied in), not as an init wrapper inside the `atlas` image.

**Rationale.** Keeps the `atlas` image a pure distroless static binary
(no shell, no `psql`, smallest attack surface). The bootstrap container
exits 0 on success; `atlas` waits on it via
`condition: service_completed_successfully`. The repo tree
(`migrations/`, `controls/`) is bind-mounted read-only at `/repo` so a
`git pull` picks up new migrations without an image rebuild.
**Confidence: high.**

### 6. Default seed via SQL, not a new CLI seed command

The default tenant / builtin scope dimension / default scope cell /
default local user are seeded by `deploy/docker/bootstrap/seed.sql` (run
by the bootstrap container as `atlas_migrate`), not by a new
`atlas-cli seed` command.

**Rationale.** There is no `tenants` table in v1 — `tenant_id` is a bare
UUID column — so a tenant "exists" purely by being referenced. The scope
and user rows are straightforward inserts with `ON CONFLICT DO NOTHING`
for idempotency. Building a CLI seed command would have been real new
product surface for no added value. The one piece that genuinely needed
platform code — a format-correct argon2id password hash — is the narrow
`atlas-cli bootstrap hash-password` helper (decision 1).
The `dimensions_hash` is computed in the shell script (`sha256sum`) and
passed to `seed.sql` via `psql -v`, so the seed has no `pgcrypto`
dependency. **Confidence: medium** — revisit if a second deployment
target (Helm, slice 038) wants the same seed; that is the moment to
consider promoting it to a real `atlas-cli` command shared by both.

## Revisit once in use

- **`/readyz` vs `/health`.** If a reverse proxy or load balancer in
  front of a real deployment needs true readiness semantics (503 until
  migrations + warm-up done), add a separate `/readyz`. `/health` stays
  liveness-only.
- **CI smoke-up.** If the bundle silently regresses (compose parses but
  the stack does not actually come up) more than once, add a nightly,
  non-PR-blocking smoke-up job — not a PR gate.
- **Bootstrap token lifecycle.** `ATLAS_BOOTSTRAP_TOKEN` is a first-boot
  convenience admin credential. The docs tell operators to rotate it, but
  nothing enforces it. Consider a TTL on fixed-token credentials, or a
  first-boot UI nudge to rotate it.
- **Seed reuse for Helm (slice 038).** When the Helm chart lands it will
  want the same default tenant/scope/user seed. Decide then whether
  `seed.sql` + `hash-password` get promoted into a shared
  `atlas-cli bootstrap seed` command both deployment targets call.
- **`scf-sample.json` vs a real SCF release.** The bootstrap imports
  `migrations/fixtures/scf-sample.json` — a small sample, not the full
  ~1,400-control SCF catalog. Once the SCF redistribution legal review
  (open question) clears and a real catalog release ships, point the
  bootstrap's `SCF_CATALOG` default at it.
- **`atlas` healthcheck depth.** The compose healthcheck for `atlas` runs
  `atlas --version` (the distroless image has no `curl`/`wget` to hit
  `/health` itself). The bootstrap container _does_ poll `/health`, so
  AC-2 is exercised on every boot — but the steady-state healthcheck is
  shallow. If a thin HTTP-probe helper is ever added to the image,
  deepen the healthcheck to hit `/health`.
- **Next.js standalone in a monorepo workspace.** `web.Dockerfile`
  assumes the standalone tracer roots the output at `web/server.js`
  because the workspace package sits under `web/`. If a Next.js upgrade
  changes the standalone output layout, the `CMD` path needs updating —
  the manual smoke test catches this.
