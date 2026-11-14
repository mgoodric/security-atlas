# Native install (no containers)

<!-- prettier-ignore-start -->
!!! info "What you'll learn"

    - How to run security-atlas with no container runtime: Go, Postgres 16 and Node only
    - The Postgres roles and the one-time superuser step the schema needs
    - Which environment variables the server, the bootstrap and the web app read
    - How to apply migrations, seed the first tenant, and sign in
<!-- prettier-ignore-end -->

The [docker-compose install](install.md) is the shortest path. This page
is for a host with no approved container runtime, such as a corporate
laptop without Docker Desktop. It runs the same pieces the compose bundle
runs, as plain processes:

| Compose service   | Native equivalent                                                 |
| ----------------- | ----------------------------------------------------------------- |
| `postgres`        | A Postgres 16 you install (Homebrew, apt, or a managed DB)        |
| `atlas-migrate`   | `deploy/docker/bootstrap/migrate.sh`, run from the repo           |
| `atlas`           | The `atlas` binary built from `cmd/atlas`                         |
| `atlas-bootstrap` | `deploy/docker/bootstrap/bootstrap.sh`, run from the repo         |
| `web`             | `npm run build && npm run start` in `web/`                        |
| `nats`, `minio`   | Optional; skipped here. See [What you give up](#what-you-give-up) |

## Prerequisites

| Requirement       | Version and source                                                                                                                |
| ----------------- | --------------------------------------------------------------------------------------------------------------------------------- |
| Go                | The toolchain `go.mod` pins. A Homebrew `go` with the default `GOTOOLCHAIN=auto` fetches it on first build.                       |
| Postgres          | 16+, with `psql` on `PATH`. On macOS: `brew install postgresql@16 && brew services start postgresql@16`.                          |
| Node.js           | The version in the repo-root `.nvmrc`, via [nvm](https://github.com/nvm-sh/nvm). Run `nvm install && nvm use` from the repo root. |
| `curl`, `openssl` | Any current version. `bootstrap.sh` polls `/health` with `curl`; `openssl` generates secrets.                                     |

No C toolchain is needed: the server and CLI build with `CGO_ENABLED=0`.

```sh
git clone https://github.com/mgoodric/security-atlas.git
cd security-atlas
```

Every command below runs from the repo root unless it says otherwise.

## Step 1 — create the database and roles

security-atlas connects as two roles, never as a superuser:

| Role            | Used by                                 | Properties                                    |
| --------------- | --------------------------------------- | --------------------------------------------- |
| `atlas_migrate` | migrations and the first-boot bootstrap | `LOGIN BYPASSRLS CREATEROLE`, owns `public`   |
| `atlas_app`     | the running server                      | `LOGIN NOSUPERUSER NOBYPASSRLS` — RLS applies |

`migrations/bootstrap/01-roles.sql` creates both (plus the `NOLOGIN`
`atlas_service_account`) and makes `atlas_migrate` the owner of schema
`public`. Postgres 15+ no longer lets ordinary roles create objects in
`public`, so that ownership change is required, and **only a superuser
can make it**. Because it takes over `public`, give security-atlas a
database of its own; never point it at a database other applications
use. Run the file once as a superuser:

```sh
# Homebrew: your macOS user is the superuser. On Linux: sudo -u postgres <command>.
createdb security_atlas
psql -d security_atlas -v ON_ERROR_STOP=1 -f migrations/bootstrap/01-roles.sql
```

Then give both roles passwords. Generate two values with
`openssl rand -hex 24` and keep them for the environment file in Step 3.
Set them with `psql`'s `\password`, which prompts for the value and sends
only its hash, so the password never appears on a command line, in
`ps` output or in shell history:

```sh
psql -d security_atlas
security_atlas=# \password atlas_migrate
security_atlas=# \password atlas_app
security_atlas=# \q
```

Check the ownership step took:

```sh
psql -d security_atlas -tAc \
  "SELECT pg_get_userbyid(nspowner) FROM pg_namespace WHERE nspname = 'public'"
# atlas_migrate
```

<!-- prettier-ignore-start -->
!!! note "Shared or managed Postgres"

    If a cluster admin pre-creates `atlas_migrate` for you as a
    non-superuser, they must also run these two statements once, as a
    superuser, before you run `01-roles.sql` as `atlas_migrate`:

    ```sql
    ALTER ROLE atlas_migrate BYPASSRLS CREATEROLE;
    ALTER SCHEMA public OWNER TO atlas_migrate;
    ```

    The schema ownership change applies to the `security_atlas` database
    only, and that database must be dedicated to security-atlas. Never
    transfer ownership of `public` in a database other applications use.

    Without them, `01-roles.sql` stops with `permission denied to create
    role` or `must be owner of schema public`. The header of
    `migrations/bootstrap/01-roles.sql` explains why each is needed.
<!-- prettier-ignore-end -->

## Step 2 — build the binaries

```sh
CGO_ENABLED=0 go build -o ./bin/ ./cmd/atlas ./cmd/atlas-cli
```

`bin/` is gitignored. `just build-go` only compile-checks and writes no
binaries.

## Step 3 — write the environment file

Save this as `atlas.env` outside the repo (it holds secrets), fill in the
two passwords from Step 1, and `source` it in every shell that runs a
step below.

```sh
# --- Postgres -------------------------------------------------------------
# migrate.sh and bootstrap.sh connect as atlas_migrate; the server as atlas_app.
export DATABASE_URL="postgres://atlas_migrate:<ATLAS_MIGRATE_PASSWORD>@localhost:5432/security_atlas?sslmode=disable"
export DATABASE_URL_APP="postgres://atlas_app:<ATLAS_APP_PASSWORD>@localhost:5432/security_atlas?sslmode=disable"

# --- Server ---------------------------------------------------------------
export BEARER_HASH_KEY="<openssl rand -hex 32>"     # server refuses to boot without it
export ATLAS_HTTP_ADDR=127.0.0.1:8080
export ATLAS_GRPC_ADDR=127.0.0.1:50051
export ATLAS_ISSUER_URL=http://localhost:8080      # turns on the OAuth token endpoint
export ATLAS_DATA_DIR="$HOME/.local/share/security-atlas"
export ATLAS_KEYSTORE_PATH="$ATLAS_DATA_DIR/keys"  # default is /var/lib/security-atlas/keys/
export ATLAS_OAUTH_TOKEN_RATE_PER_MIN=600          # headroom for the bootstrap's control upload

# --- First-boot bootstrap -------------------------------------------------
export REPO_ROOT="$PWD"                            # the scripts default to /repo
export ATLAS_HTTP_URL=http://localhost:8080        # also read by the web app
export ATLAS_BOOTSTRAP_TENANT=00000000-0000-4000-8000-000000000001
export ATLAS_DEFAULT_USER_EMAIL=you@example.com
export ATLAS_DEFAULT_USER_PASSWORD="<the password you will sign in with>"
export PATH="$PWD/bin:$PATH"                       # bootstrap.sh calls atlas-cli
```

`REPO_ROOT` and `PATH` use `$PWD`, so source the file from the repo root.
Every variable is described in the
[configuration reference](configuration.md). The compose-only ones
(`POSTGRES_PASSWORD`, `MINIO_*`, the host-port variables) do not apply here.

```sh
chmod 600 /path/to/atlas.env
source /path/to/atlas.env
mkdir -p "$ATLAS_DATA_DIR" && chmod 700 "$ATLAS_DATA_DIR"
```

Leave `ATLAS_APP_PASSWORD` unset: you set the `atlas_app` password in
Step 1. When it is set, `migrate.sh` re-applies it with
`ALTER ROLE ... PASSWORD`, which passes the value on `psql`'s command
line.

## Step 4 — apply migrations

```sh
sh deploy/docker/bootstrap/migrate.sh
# ...
# [migrate] migrate complete — schema is current
```

This is the script the compose `atlas-migrate` service runs. It re-runs
`01-roles.sql` as a no-op, records every applied file in a
`schema_migrations` table, and skips files already recorded, so it is
safe to run on every upgrade. Prefer it over `just migrate-up`, which
re-applies every file and is meant for a throwaway dev database.

## Step 5 — start the server

```sh
atlas
# atlas: gRPC listening on 127.0.0.1:50051
# atlas: HTTP listening on 127.0.0.1:8080
```

Leave it running (a second terminal, `tmux`, or a launchd/systemd unit),
then confirm it is ready:

```sh
curl -s http://localhost:8080/ready
# {"status":"ready","db":"ok"}
```

## Step 6 — seed the first tenant

In another shell with `atlas.env` sourced:

```sh
sh deploy/docker/bootstrap/bootstrap.sh
# ...
# [bootstrap] uploaded 50 control bundles
# [bootstrap] bootstrap complete — security-atlas is seeded and ready
```

This is the compose `atlas-bootstrap` service. It seeds the default
tenant, scope cell and local user, imports the bundled SCF sample and the
SOC 2 crosswalk, then uploads the 50 SOC 2 control bundles through an
OAuth client whose credentials it keeps in
`$ATLAS_DATA_DIR/oauth-bootstrap-credentials.json` (mode 0600). It is
idempotent, and it prints the `atlas-cli catalog import-crosswalk`
command for each other bundled framework.

## Step 7 — run the web app

```sh
nvm use
npm ci
cd web
npm run build
PORT=3000 npm run start
```

The app reads `ATLAS_HTTP_URL` (sourced from `atlas.env`) for its
server-side calls to the API; it defaults to `http://localhost:8080`.
Leave `NEXT_PUBLIC_API_BASE_URL` unset.

## Step 8 — sign in

Open <http://localhost:3000> and sign in with `ATLAS_DEFAULT_USER_EMAIL`
and `ATLAS_DEFAULT_USER_PASSWORD`. Change the password after the first
sign-in. Keep `ATLAS_SECURE_COOKIES` unset while you serve plain HTTP;
set it to `true` once the host is behind TLS.

## Upgrades

```sh
git pull
source /path/to/atlas.env
CGO_ENABLED=0 go build -o ./bin/ ./cmd/atlas ./cmd/atlas-cli
sh deploy/docker/bootstrap/migrate.sh      # before restarting the server
# restart atlas
nvm use && npm ci                          # at the repo root, as in Step 7
(cd web && npm run build)                  # then restart the web app
```

Run `migrate.sh` before the new server starts. A newer binary against an
older schema fails at runtime, not at boot. See the
[upgrade guide](upgrade.md) for release notes and rollback.

## What you give up

Without NATS, evidence push writes straight to the ledger in-process,
with no durable buffer, and the on-ingest re-evaluation reactions are
off; the hourly recompute still runs. Point `NATS_URL` at a JetStream
server to restore them. The
[optional services table](https://github.com/mgoodric/security-atlas/blob/main/docs/SELF_HOSTING.md)
in `docs/SELF_HOSTING.md` lists the effect of leaving out each optional
dependency (NATS, an S3-compatible store, the OSCAL bridge, OTLP).

## Next steps

- [Framework setup →](framework-setup.md) — load more frameworks on top
  of SOC 2
- [Configuration reference](configuration.md) — every environment variable

---

## Was this helpful?

Tell us in [GitHub Discussions](https://github.com/mgoodric/security-atlas/discussions).
