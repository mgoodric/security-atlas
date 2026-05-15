# 073 — First-time login UX + bootstrap-token discoverability

**Cluster:** Auth
**Estimate:** 1.5d
**Type:** JUDGMENT

## Narrative

A first-time user installing the docker-compose self-host bundle (slice 037) or the Helm chart (slice 038) lands on `/login` and sees the current copy:

> _Paste a bearer token issued by `atlas-cli credentials issue` or printed to stderr at server startup._

For the developer who set up the install, this is sufficient — they ran `atlas-cli` and they know where stderr is. For a security engineer who just executed `helm install` and pointed their browser at the LoadBalancer, this is a dead end: they don't have an `atlas-cli` binary on hand, they don't necessarily have `kubectl logs` ergonomics rehearsed, and they have no way to know whether the bootstrap step has already run or is still pending.

**The actual flow on a clean install** (per slice 037 `bootstrap.sh` + the cmd/atlas startup path):

1. `bootstrap.sh` runs `ALTER ROLE atlas_app PASSWORD ...`, applies migrations, then mints a one-time bootstrap admin bearer token by calling `atlas-cli credentials issue --role admin --bootstrap`.
2. The token is printed to **bootstrap.sh's stdout** (typically captured into `docker compose logs atlas` or `kubectl logs deploy/atlas`).
3. After consumption, the token is single-use; further token issuance goes through the running atlas's admin API once a session is established.

The discoverability problem is steps 2 → 3: the user doesn't know to look in the logs, doesn't know how to filter for the token, and has no fallback if the logs have rolled.

This slice closes that gap on three fronts:

**1. Login-page copy + first-boot detection (frontend + atlas server):**
The login page detects whether the platform has ever had a successful sign-in (a single SELECT on a fresh "first sign-in completed at" marker — populated on the first successful `signIn` action, never updated afterwards). On a fresh install, the login card swaps to a different mode that shows clear platform-specific guidance:

> **First time signing in?** The bootstrap admin token was generated when the platform started. Find it with one of:
>
> - **docker-compose:** `docker compose logs atlas 2>&1 | grep BOOTSTRAP_TOKEN`
> - **Helm:** `kubectl logs deploy/atlas --tail=200 2>&1 | grep BOOTSTRAP_TOKEN`
> - **Bare binary:** look in the stderr of the `atlas` process you launched
>
> If the token has scrolled out of the log buffer, see _Recovering the bootstrap token_ in [Troubleshooting → First-time login](https://...).

The detection is a one-row SELECT on a `platform_status` table (new) reading `first_signin_at`; absence means "fresh install". This bypasses listing actual users (which would require admin auth) and is RLS-safe (the table has a single row, no `tenant_id`, public-read).

**2. Bootstrap script: emit a discoverable marker line (`bootstrap.sh` + `cmd/atlas`):**
The current `bootstrap.sh` prints the token inline somewhere in its phase output. This slice changes the emission shape to a single, grep-friendly line:

    ATLAS_BOOTSTRAP_TOKEN=<token>  # one-time use, expires <ts>, see docs/...

…and ALSO writes the same token to a file at `${ATLAS_DATA_DIR:-./atlas-data}/bootstrap-token` with mode `0600`. The file is deleted (by atlas itself, NOT the bootstrap script) the moment the token is consumed for the first successful sign-in. This gives the user three orthogonal ways to find the token: log grep, file inspection, or stderr-of-atlas. The script's last-line output reminds the user explicitly.

**3. Troubleshooting page in docs (slice 058 site):**
A new `docs-site/docs/troubleshooting/first-login.md` walks through every failure mode:

- _Token has scrolled out of the log buffer_ — how to inspect the bootstrap-token file
- _Token was already consumed but no session was established_ — how to invalidate the consumed marker and re-mint (`atlas-cli credentials issue --role admin --reset-bootstrap`, new flag in this slice)
- _The platform is up but `/login` returns 500 in fresh-install mode_ — what the `platform_status` table looks like, how to seed it manually (`INSERT INTO platform_status DEFAULT VALUES;`)
- _I'm running a multi-tenant install and I don't know which tenant the bootstrap admin is in_ — explanation of the single-tenant invariant (canvas §5.4 + OQ #13 resolution), with the SQL to verify

The slice's HARD ANTI-CRITERION (P0-A1) is that the bootstrap-token file is **deleted on first successful use**. Long-lived bootstrap tokens on disk are a credential leak shape we are not introducing. The single-use guarantee already exists in atlas's issue path (the token is `bootstrap_consumed_at` once it's used); this slice extends that to deleting the file artifact too.

## Acceptance criteria

- [ ] AC-1: New migration `migrations/sql/20260516000000_platform_status.sql` adds a single-row table `platform_status` with columns `singleton_lock bool PRIMARY KEY DEFAULT true`, `first_signin_at timestamptz NULL`, `bootstrap_token_consumed_at timestamptz NULL`. The `singleton_lock` is a `UNIQUE` constraint that admits only one row (`CHECK (singleton_lock IS TRUE)`). RLS: `tenant_id IS NULL` (no tenant), public-read policy, write-only via atlas server's elevated path (NOT the app pool). The migration is reversible (DOWN drops the table).
- [ ] AC-2: `internal/platform/status.go` (new package) exports `IsFirstInstall(ctx) (bool, error)` (reads `first_signin_at IS NULL`) and `MarkFirstSignin(ctx, at time.Time) error` (UPDATE on the singleton row; idempotent — only writes if `first_signin_at IS NULL`).
- [ ] AC-3: `cmd/atlas/main.go` adds a public `GET /v1/install-state` endpoint that returns `{ "first_install": bool }`. No auth; no tenant context; same public-metadata precedent as slice 072's `/v1/version`. `Cache-Control: no-store` (this flips at first sign-in and the UI needs to see it flip).
- [ ] AC-4: `web/app/login/page.tsx` reads `/v1/install-state` server-side (SSR) on render. If `first_install: true`, renders the "First time signing in?" guidance block (per the narrative) above the token input. If `false`, renders the existing copy unchanged. The guidance block is a `<Card>` with role-appropriate semantics; copy is the exact three-bullet form in the narrative with the docs-site troubleshooting link.
- [ ] AC-5: `web/app/login/actions.ts` `signIn` action, on successful authentication, calls `MarkFirstSignin` via a new BFF route `POST /api/install/mark-first-signin` (which proxies to a new atlas endpoint `POST /v1/install/_internal/mark-first-signin` — the `_internal` prefix denoting elevated-only-from-server-context; the BFF route enforces a server-side bearer reset-of-bearer-token check before forwarding so an attacker without a bearer can't flip the flag). Idempotent: subsequent successful sign-ins are no-ops.
- [ ] AC-6: `deploy/docker/bootstrap.sh` emits exactly one line of the form `ATLAS_BOOTSTRAP_TOKEN=<token>  # one-time use, expires <ISO-8601>, docs: <docs-site-link>` to stdout when it mints the bootstrap token. The script also writes the token (token only — no surrounding text) to `${ATLAS_DATA_DIR:-/var/lib/atlas}/bootstrap-token` with `chmod 0600`. The script's final summary line reminds the user where the file is.
- [ ] AC-7: `cmd/atlas/main.go` watches for `${ATLAS_DATA_DIR}/bootstrap-token` at startup and, on the first successful sign-in, deletes that file atomically (rename to a tmp path then unlink). If the file does not exist, this is a no-op. Logged at INFO level: `"bootstrap-token file consumed and deleted"`. Tested.
- [ ] AC-8: `atlas-cli credentials issue --role admin --reset-bootstrap` (new flag) restores the bootstrap-token file (re-mints + re-writes the file) AND clears `platform_status.bootstrap_token_consumed_at`. Returns an error if `first_signin_at IS NOT NULL` AND no `--force` flag is given (rationale: re-issuing bootstrap after a real user has signed in is a foot-gun; require explicit intent).
- [ ] AC-9: `docs-site/docs/troubleshooting/first-login.md` exists with the five failure-mode subsections from the narrative. Each subsection ends with "Verified on docker-compose: ..." and "Verified on Helm: ..." command-line snippets. The page is added to the mkdocs nav under a new "Troubleshooting" top-level section.
- [ ] AC-10: `docs-site/docs/install.md` (slice 058) gets a "First-time sign-in" callout pointing at the troubleshooting page. The callout uses the mkdocs Material admonition syntax, wrapped per slice-058's prettier-ignore convention.
- [ ] AC-11: README.md "Self-hosting" section adds a "Your first sign-in" paragraph: where the token comes from, the three ways to find it (log grep / file inspection / stderr), and a pointer to the troubleshooting page.
- [ ] AC-12: A new Playwright spec `web/e2e/first-time-login.spec.ts` (under slice 069's runner) asserts: (a) on a stack where `/v1/install-state` returns `first_install: true`, the login page renders the guidance block; (b) on a stack where it returns `false`, the guidance block is absent. Uses route mocking (`page.route()`) rather than requiring a real fresh-install fixture — the seed-data harness gap from slice 069's AC-5 PARTIAL still applies, so we mock the upstream rather than block on it.
- [ ] AC-13: `web/app/api/install/mark-first-signin/route.test.ts` (vitest) covers the BFF route: happy path, missing-bearer-cookie 401, upstream-error 502 translation, idempotent re-call.
- [ ] AC-14: Integration test `internal/platform/status_integration_test.go` covers: (a) fresh table → `IsFirstInstall = true`; (b) `MarkFirstSignin` flips it to `false`; (c) second `MarkFirstSignin` call is a no-op (timestamp unchanged); (d) `singleton_lock` uniqueness — second `INSERT` fails. Real DB; RLS-policy verified positive (public read) and negative (cross-context write rejected from app pool).
- [ ] AC-15: `docs/audit-log/073-first-time-login-decisions.md` records the JUDGMENT-slice decisions, particularly: (1) the `_internal` endpoint prefix vs first-class admin-only route, (2) the `--reset-bootstrap --force` foot-gun threshold (where exactly does "real user has signed in" mean — first user vs admin user vs any session?), (3) the bootstrap-token file path default (`/var/lib/atlas/bootstrap-token` vs `./atlas-data/bootstrap-token` — the docker-compose vs Helm reality check), and (4) what to log when the file is consumed (INFO with the truncated token hash? Or just "consumed"?).

## Constitutional invariants honored

- **Tenant isolation (invariant 6)**: `platform_status` is a singleton, tenant-agnostic table; its single row is public-readable BUT write-only-via-elevated-pool. The `tenant_id IS NULL OR current_tenant_matches(tenant_id)` RLS pattern from slice 068 applies: this row has `tenant_id IS NULL`.
- **AI-assist boundary**: nothing in this slice is AI-generated content. All the troubleshooting prose is human-authored runbook-style; all the diagnostic commands are real `grep`/`kubectl`/`docker compose` invocations.
- **Working norms — Markdown over prose** (CLAUDE.md): troubleshooting page uses tables, lists, fenced code blocks; the login-page guidance block is a 3-bullet list, not a paragraph.

## Canvas references

- `Plans/canvas/05-scopes.md §5.4` — tenant isolation for `platform_status` (the singleton table sits outside the tenancy model intentionally)
- `Plans/canvas/11-open-questions.md` item 13 (RESOLVED) — single-tenant operator vs multi-tenant data model is load-bearing for the troubleshooting page's "I'm running multi-tenant" subsection

## Dependencies

- **034** (OIDC RP + local users + api_keys admin) — the bearer-token issuance path this slice extends
- **037** (docker-compose self-host bundle) — `bootstrap.sh` to modify
- **038** (Helm chart for K8s) — `kubectl logs` flavor of the docs
- **058** (user docs scaffold) — `troubleshooting/first-login.md` lands in this site
- **069** (verification suite) — vitest + Playwright runners

## Anti-criteria (P0 — block merge)

- **P0-A1**: Does NOT leave the bootstrap-token file on disk indefinitely. AC-7's atomic-delete on first sign-in is the load-bearing safety property. A long-lived `bootstrap-token` file is a credential leak shape; the file's reason to exist is exactly "make the FIRST sign-in discoverable" and no longer. The Playwright spec (AC-12) verifies the file path is non-existent post-sign-in.
- **P0-A2**: Does NOT log the bootstrap token plaintext after `bootstrap.sh` consumption. The one place it's allowed to appear in plaintext is the `ATLAS_BOOTSTRAP_TOKEN=...` stdout line from `bootstrap.sh` (which is the entire point) and the file at AC-6 (mode 0600). Atlas's own logs never echo it.
- **P0-A3**: Does NOT add a "phone home to verify install" surface. The `/v1/install-state` endpoint is read-only platform metadata, just like `/v1/version`; nothing in this slice initiates an outbound network call.
- **P0-A4**: Does NOT add auth to `/v1/install-state`. The endpoint is intentionally public; "is this a fresh install?" is metadata, NOT tenant data. Same precedent as `/v1/version` (slice 072) and `/health` (slice 037). The handler comment documents the intent.
- **P0-A5**: Does NOT change the existing bearer-token sign-in flow for non-fresh installs. The login page renders identically once `first_install: false`; the existing copy is preserved verbatim. Regressions on the production sign-in path are unacceptable.
- **P0-A6**: Does NOT make `--reset-bootstrap` a default-easy operation. The `--force` flag for re-issuance after a real user has signed in is INTENTIONAL friction; the path is a recovery tool, not an everyday operation.
- **P0-A7**: Does NOT couple `platform_status` to tenancy. The table sits outside the tenant model on purpose (it describes the platform, not a tenant within it). Adding a `tenant_id` column to it would (a) require multi-tenant onboarding to flip the marker N times, (b) confuse the single-tenant operator's mental model. Slice 068's RLS pattern (`tenant_id IS NULL OR current_tenant_matches(tenant_id)`) handles the singleton case cleanly.

## Skill mix (3–5)

- Go HTTP handler patterns (small public read endpoint, small elevated write endpoint, server-side file deletion atomic-rename pattern)
- shadcn/ui form composition (the login card's two-mode render shape)
- Migration design with singleton-row UNIQUE pattern (canonical-singleton tables are a class; getting the constraint shape right matters)
- `engineering-advanced-skills:runbook-generator` (the troubleshooting page IS a runbook; treat it that way)
- `security-review` (everything that touches credentials needs a fresh look; specifically: the file mode, the deletion atomicity, the `_internal` prefix mismatching to actual access control)

## Notes for the implementing agent

- The most subtle correctness call: AC-5's `_internal` prefix. It is NOT an access control; it is a naming convention. Real access control is server-side bearer-revalidation in the BFF route. If the engineer's grill surfaces a cleaner shape (e.g., admin-only middleware on a non-prefixed route), use it and record the call in the decisions log.
- The `bootstrap-token` file path default is the dominant judgment call: `/var/lib/atlas/bootstrap-token` is the FHS-correct path for a daemon's mutable state, but the docker-compose bundle (slice 037) defaults `ATLAS_DATA_DIR=./atlas-data` for solo developers running locally. Make the env var the source of truth; fall back to `/var/lib/atlas` only if `ATLAS_DATA_DIR` is unset AND the binary is running as a non-host user (heuristic: `os.Getuid() != geteuid_of_invoker` is not portable; use `os.Getenv("ATLAS_DATA_DIR")` first and `/var/lib/atlas` second, document both paths in the troubleshooting page).
- For the Playwright spec (AC-12), the `page.route()` mocking pattern lets you exercise both UI states without needing a real fresh-install stack. This is the same `seed-data-harness gap` workaround pattern slice 069's `e2e/fixtures.ts` uses; align with that fixture's shape so a future seed-data slice can replace the mock without rewriting this spec.
- The migration's RLS policy is the trickiest part — singleton tables outside the tenancy model are rare. The slice 068 schema-registry pattern (`tenant_id IS NULL OR current_tenant_matches(tenant_id)`) is the closest precedent; lift it. Two policies (public-read, elevated-write) under `FORCE ROW LEVEL SECURITY`.
