# Configuration reference

This page is the single human-readable reference for every environment
variable a self-hosted security-atlas deployment reads. The
copy-and-edit template is
[`deploy/docker/.env.example`](https://github.com/mgoodric/security-atlas/blob/main/deploy/docker/.env.example);
the bring-up walkthrough is the
[Install quickstart](install.md) and the deeper
[`docs/SELF_HOSTING.md`](https://github.com/mgoodric/security-atlas/blob/main/docs/SELF_HOSTING.md)
guide. This page **documents** those variables; it never replaces the
template — copy `.env.example` to `.env`, then use this page to decide
what each value should be.

<!-- prettier-ignore-start -->
!!! info "How this page stays accurate"

    The variable set below is kept in lockstep with
    `deploy/docker/.env.example` by a drift guard —
    [`scripts/check-config-reference-drift.sh`](https://github.com/mgoodric/security-atlas/blob/main/scripts/check-config-reference-drift.sh)
    (run it locally with `just config-reference-drift-check`). It fails
    if a key in the template is missing from this page, or if this page
    documents a variable the template does not define. The same check
    runs in CI on every PR that touches the page or the template, so the
    reference cannot silently drift from the deployable surface.
<!-- prettier-ignore-end -->

## Reading the table

- **Variable** — the environment-variable name as it appears in
  `.env.example`.
- **Default** — the value shipped in the template. `CHANGE_ME` means
  _you must supply a value_; there is no usable default. For
  secret-typed variables the default column shows a placeholder only —
  this page never prints a real or copy-pasteable credential.
- **Required?** — `yes` means the stack refuses to start until it is
  set; `no` means a sensible default applies and you override only if
  you need to.
- **Scope** — which component reads it: **server** (the `atlas` binary
  and its companion services), **web** (the Next.js frontend), or
  **bootstrap** (the one-shot `atlas-bootstrap` container that seeds the
  first tenant, user, and catalog).

## Security-critical variables

A configuration reference is dangerous precisely because operators
treat it as authoritative. The following variables must be set
deliberately — leaving them at a guessable value or an unsafe toggle is
a deployment vulnerability, not a convenience.

<!-- prettier-ignore-start -->
!!! danger "Set every secret to a unique high-entropy value"

    These variables are **secrets**. Generate each one with
    `openssl rand -hex 32` (a fresh value per variable) and never reuse
    a value across deployments. Never commit a real value — `.env` is
    gitignored for this reason.

    - `POSTGRES_PASSWORD`
    - `ATLAS_APP_PASSWORD` (and the password segment of `DATABASE_URL_APP`, which must match it)
    - `MINIO_ROOT_PASSWORD`
    - `BEARER_HASH_KEY`
    - `ATLAS_BOOTSTRAP_TOKEN`
    - `ATLAS_DEFAULT_USER_PASSWORD`

!!! danger "These toggles change the security posture of the deployment"

    - **`ATLAS_TEST_MODE`** — when set, mounts `POST /v1/test/issue-jwt`,
      which mints arbitrary admin-claim JWTs to any client that can
      reach the server. **DO NOT set this in production.** The default
      (empty) keeps production safe.
    - **`ATLAS_METRICS_FALLBACK_ENABLE`** — when `true`, exposes an
      **unauthenticated** `/metrics` read surface. It is **off by
      default**; if you turn it on, it MUST be gated at the network
      layer (firewall, reverse-proxy ACL, or private subnet).
    - **`ATLAS_SECURE_COOKIES`** — set this to `true` once the
      deployment is behind TLS, so session cookies carry the `Secure`
      flag. A `Secure` cookie over plain HTTP is silently dropped by
      browsers; the default `false` is for local HTTP only.
    - **`TRUSTED_PROXY_CIDRS`** — comma-separated CIDR allowlist of the
      reverse-proxy address(es) atlas sits behind. The server walks
      `X-Forwarded-For` **right-to-left**, accepting a hop only while the
      connecting peer is inside one of these CIDRs and stopping at the
      first untrusted hop (the real client). **Off by default** (shipped
      commented out → no proxy trusted → direct TCP peer). A client that
      is not behind one of these proxies **cannot** spoof its source IP by
      forging the header. **SECURITY:** list only your own proxy
      addresses; `0.0.0.0/0` trusts every connection and reopens the
      spoofing vector.
    - **`TRUST_FORWARDED_HEADERS`** — **deprecated** boolean predecessor
      of `TRUSTED_PROXY_CIDRS`. When set to `1` (and `TRUSTED_PROXY_CIDRS`
      is unset) it is mapped to **trust any proxy** (`0.0.0.0/0` + `::/0`)
      for back-compat and the server logs a deprecation warning at boot.
      `X-Forwarded-For` is a plain request header any client can set, so
      trusting any proxy without a header-scrubbing reverse proxy in front
      is a **client-IP spoofing vector**. **Off by default** (shipped
      commented out). Prefer `TRUSTED_PROXY_CIDRS`.
<!-- prettier-ignore-end -->

## Required

The stack refuses to start until each of these is set. Every
secret-typed entry must be a fresh `openssl rand -hex 32` value.

| Variable                      | Default                                                                       | Required? | Scope             | Description                                                                                                                                                                                                              |
| ----------------------------- | ----------------------------------------------------------------------------- | --------- | ----------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `POSTGRES_PASSWORD`           | `CHANGE_ME`                                                                   | yes       | server            | Postgres superuser password; used by the postgres container only. Secret — supply via `openssl rand -hex 32`.                                                                                                            |
| `ATLAS_APP_PASSWORD`          | `CHANGE_ME`                                                                   | yes       | server, bootstrap | Password for the `atlas_app` role the server connects with. The bootstrap container sets it on the role via `ALTER ROLE`; must match the password segment of `DATABASE_URL_APP`. Secret.                                 |
| `MINIO_ROOT_USER`             | `CHANGE_ME`                                                                   | yes       | server            | MinIO root user; doubles as the S3 access key the server uses for the artifacts bucket.                                                                                                                                  |
| `MINIO_ROOT_PASSWORD`         | `CHANGE_ME`                                                                   | yes       | server            | MinIO root password; doubles as the S3 secret key. Secret — supply via `openssl rand -hex 32`.                                                                                                                           |
| `BEARER_HASH_KEY`             | `CHANGE_ME`                                                                   | yes       | server            | HMAC key for `api_keys.token_hash` (slice 034). The server refuses to boot without it; must be ≥ 32 bytes. Rotating it invalidates every issued API key. Secret.                                                         |
| `ATLAS_BOOTSTRAP_TOKEN`       | `CHANGE_ME`                                                                   | yes       | bootstrap         | Pre-shared admin bearer the one-shot bootstrap container uses to upload control bundles. A convenience credential — **rotate or revoke it after first boot** once a real operator credential exists. Secret.             |
| `ATLAS_DEFAULT_USER_EMAIL`    | `admin@example.com`                                                           | yes       | bootstrap         | Email of the default local-mode account created on first boot (no external IdP required).                                                                                                                                |
| `ATLAS_DEFAULT_USER_PASSWORD` | `CHANGE_ME`                                                                   | yes       | bootstrap         | Password for the default local-mode account. **Change it immediately after first sign-in.** Secret.                                                                                                                      |
| `DATABASE_URL_APP`            | `postgres://atlas_app:CHANGE_ME@postgres:5432/security_atlas?sslmode=disable` | yes       | server, bootstrap | Connection string for the runtime `atlas_app` role (RLS-enforced — constitutional invariant #6). Replace the `CHANGE_ME` password segment with the same value as `ATLAS_APP_PASSWORD`. The password segment is a secret. |
| `DATABASE_URL_MIGRATE`        | `postgres://atlas_migrate@postgres:5432/security_atlas?sslmode=disable`       | yes       | bootstrap         | Connection string for the `atlas_migrate` role used to apply migrations. It authenticates via `trust` on the container network and **carries no password** — do not add a password segment.                              |

## Database

| Variable      | Default          | Required? | Scope             | Description                                                                                     |
| ------------- | ---------------- | --------- | ----------------- | ----------------------------------------------------------------------------------------------- |
| `POSTGRES_DB` | `security_atlas` | no        | server, bootstrap | Postgres database name. Change only if you point at an existing database with a different name. |

## Object store and NATS

The object store holds evidence artifacts larger than 1 MB; NATS
JetStream is the durable event substrate. Their host ports are in the
[Ports](#ports) section.

| Variable           | Default            | Required? | Scope  | Description                                                                                                                                                                                                                                                                                                  |
| ------------------ | ------------------ | --------- | ------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `ARTIFACTS_BUCKET` | `atlas-artifacts`  | no        | server | S3 bucket name for evidence artifacts > 1 MB (slice 036).                                                                                                                                                                                                                                                    |
| `AWS_REGION`       | `us-east-1`        | no        | server | Region label sent to the S3 client. MinIO ignores it; any value works. Relevant only when pointing at real AWS S3.                                                                                                                                                                                           |
| `NATS_URL`         | `nats://nats:4222` | no        | server | NATS JetStream evidence-ingest substrate (slice 015). Shipped commented; the bundled `nats` service address is the default. Override **only** to point at an external NATS cluster. When it resolves empty the server falls back to in-process dev mode with no durable buffer — the bundle never does this. |

## Cookies and security

| Variable                  | Default         | Required? | Scope  | Description                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                  |
| ------------------------- | --------------- | --------- | ------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `ATLAS_SECURE_COOKIES`    | `false`         | no        | server | Marks session cookies `Secure`. Set to `true` once the deployment is behind TLS. A `Secure` cookie over plain HTTP is silently dropped — the `false` default is for local HTTP only. See the [security-critical](#security-critical-variables) note.                                                                                                                                                                                                                                                                                                                                                                                         |
| `TRUSTED_PROXY_CIDRS`     | _(unset → off)_ | no        | server | Comma-separated CIDR allowlist of the reverse proxy/proxies atlas sits behind (slice 466). The server walks `X-Forwarded-For` **right-to-left**, accepting a hop only while the connecting peer is inside one of these CIDRs, and stops at the first untrusted hop (the real client) — so a client not behind a listed proxy cannot spoof its source IP. Shipped commented out; **off by default** (no proxy trusted → direct TCP peer `r.RemoteAddr`, byte-identical to unset). **Security:** list only your own proxy addresses — `0.0.0.0/0` reopens the spoofing vector. See the [security-critical](#security-critical-variables) note. |
| `TRUST_FORWARDED_HEADERS` | _(unset → off)_ | no        | server | **Deprecated** boolean predecessor of `TRUSTED_PROXY_CIDRS` (slice 465 → 466). When set to `1` and `TRUSTED_PROXY_CIDRS` is unset, it is mapped to **trust any proxy** (`0.0.0.0/0` + `::/0`) for back-compat and the server logs a deprecation warning at boot; `TRUSTED_PROXY_CIDRS` takes precedence when both are set. Shipped commented out; **off by default**. Trusting any proxy without a header-scrubbing reverse proxy in front is a client-IP spoofing vector — prefer `TRUSTED_PROXY_CIDRS`. See the [security-critical](#security-critical-variables) note.                                                                    |

## Observability

security-atlas emits OpenTelemetry natively. With no OTEL endpoint set,
the SDK runs in **no-op mode** — no traces or metrics are exported and
there is zero overhead. Export is strictly opt-in. The variables below
are shipped commented out in the template (default off); the standard
`OTEL_*` variables follow ordinary OpenTelemetry semantics — atlas adds
none of its own.

| Variable                        | Default                 | Required? | Scope  | Description                                                                                                                                                                                                                                                                                                                                |
| ------------------------------- | ----------------------- | --------- | ------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `OTEL_EXPORTER_OTLP_ENDPOINT`   | _(unset → no-op)_       | no        | server | OTLP endpoint for the OTel Collector. When unset, the SDK exports nothing. Set it (e.g. `http://otel-collector:4317`) to enable trace + metric export. The companion `deploy/observability/` bundle ships the receive side.                                                                                                                |
| `ATLAS_DEPLOYMENT_ENVIRONMENT`  | _(unset → omitted)_     | no        | server | Free-form label that becomes the `deployment.environment` resource attribute on every span/metric (e.g. `homelab`, `staging`, `production`). When unset, the attribute is omitted.                                                                                                                                                         |
| `ATLAS_METRICS_FALLBACK_ENABLE` | `false`                 | no        | server | Opt-in Prometheus `/metrics` scrape endpoint, served **unauthenticated** (same auth-bypass as `/health`). **Off by default.** Turning it on creates an unauthenticated read surface that MUST be network-gated. Prefer the OTLP push path (`OTEL_EXPORTER_OTLP_ENDPOINT`). See the [security-critical](#security-critical-variables) note. |
| `OTEL_EXPORTER_OTLP_PROTOCOL`   | _(unset → SDK default)_ | no        | server | OTLP transport: `grpc` (default) or `http/protobuf`. Match your collector's receiver. Standard OpenTelemetry semantics; atlas adds nothing.                                                                                                                                                                                                |
| `OTEL_SERVICE_NAME`             | _(unset → SDK default)_ | no        | server | Logical service name stamped on every emitted span/metric. Standard OpenTelemetry semantics.                                                                                                                                                                                                                                               |
| `OTEL_RESOURCE_ATTRIBUTES`      | _(unset → omitted)_     | no        | server | Comma-separated `key=value` resource attributes merged onto every span/metric (e.g. `service.namespace=grc,service.version=1.2.3`). Standard OpenTelemetry semantics. `ATLAS_DEPLOYMENT_ENVIRONMENT` is the atlas-specific shortcut for the `deployment.environment` attribute.                                                            |
| `OTEL_TRACES_SAMPLER`           | _(unset → SDK default)_ | no        | server | Trace sampler name (standard OTel values, e.g. `parentbased_traceidratio`). Standard OpenTelemetry semantics.                                                                                                                                                                                                                              |
| `OTEL_TRACES_SAMPLER_ARG`       | _(unset → SDK default)_ | no        | server | Argument for `OTEL_TRACES_SAMPLER` (e.g. `0.1` for a 10% ratio sampler). Standard OpenTelemetry semantics.                                                                                                                                                                                                                                 |

## Web

| Variable                   | Default   | Required? | Scope | Description                                                                                                                                                                                                                                                                                                              |
| -------------------------- | --------- | --------- | ----- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `NEXT_PUBLIC_API_BASE_URL` | _(empty)_ | no        | web   | The API base URL the **browser** uses to reach the atlas HTTP API. Leave empty when a reverse proxy fronts both `web` and `atlas` under one hostname (the browser uses same-origin relative URLs). Set an absolute URL only when the browser must reach atlas at a different origin (e.g. dev: `http://localhost:8080`). |

## Email / SMTP notification channel

The email delivery channel (slice 445) sends a daily digest of a user's unread
in-app notifications to their account email — but **only** if that user has
opted in (Settings → Notifications → Email delivery; default **off**) and the
deployment has SMTP configured. The digest carries summary counts + a deep-link
back into the authenticated app — never the notification details. When
`ATLAS_SMTP_HOST` is unset (the default) the channel is **inert**: no mail is
ever sent. The channel sends only; it never receives mail. `ATLAS_SMTP_PASSWORD`
is read from the environment only and is never logged.

| Variable                | Default   | Required? | Scope  | Description                                                                                                                                                                    |
| ----------------------- | --------- | --------- | ------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `ATLAS_SMTP_HOST`       | _(unset)_ | no        | server | SMTP relay host. When unset the email channel is inert (no mail sent). Set host + sender to enable email delivery.                                                             |
| `ATLAS_SMTP_PORT`       | `587`     | no        | server | SMTP submission port. STARTTLS is used opportunistically when the server advertises it.                                                                                        |
| `ATLAS_SMTP_SENDER`     | _(unset)_ | no        | server | The `From:` address the digest is sent from. Required (together with host) to enable delivery.                                                                                 |
| `ATLAS_SMTP_USERNAME`   | _(unset)_ | no        | server | SMTP auth username. When set, the channel authenticates with PLAIN auth (over STARTTLS) before sending.                                                                        |
| `ATLAS_SMTP_PASSWORD`   | _(unset)_ | no        | server | SMTP auth password. **Secret** — supply a dedicated, least-privilege send-only credential; never logged. Generate/scope per your provider (e.g. `openssl rand -hex 32`).       |
| `ATLAS_SMTP_TIMEOUT`    | `10s`     | no        | server | Wall-clock cap on a single SMTP dial+send (Go duration string). A slow/unreachable relay fails fast; failures are recorded and re-attempted on the next tick (no hot retry).   |
| `ATLAS_PUBLIC_BASE_URL` | _(unset)_ | no        | server | Public base URL of the authenticated app, used to build the digest's "Open your notifications" deep-link. When unset, the link falls back to a relative `/notifications` path. |

## Test-mode

<!-- prettier-ignore-start -->
!!! danger "ATLAS_TEST_MODE mints admin JWTs — never set it in production"

    `ATLAS_TEST_MODE=1` mounts `POST /v1/test/issue-jwt`, which returns a
    1-hour admin-claim JWT for any `{tenant_id, user_id, roles, super_admin}`
    body — to any HTTP client that can reach the server. It exists for the
    Playwright e2e harness. The empty default keeps production safe.
<!-- prettier-ignore-end -->

| Variable          | Default   | Required? | Scope       | Description                                                                                                                                   |
| ----------------- | --------- | --------- | ----------- | --------------------------------------------------------------------------------------------------------------------------------------------- |
| `ATLAS_TEST_MODE` | _(empty)_ | no        | server, web | When set to `1`, mounts the e2e JWT-issuance endpoint `POST /v1/test/issue-jwt` (slice 201). **DO NOT set in production — mints admin JWTs.** |

## Ports

Host-side port mappings. Change these only on a port conflict — the
service-to-service addresses inside the compose network are fixed. When
exposing any of these on a public interface, gate admin/internal ports
(`NATS_MONITOR_PORT`, `MINIO_CONSOLE_PORT`, `ATLAS_GRPC_PORT`) behind a
firewall or reverse proxy rather than binding them to `0.0.0.0`.

| Variable             | Default | Required? | Scope  | Description                                                                                            |
| -------------------- | ------- | --------- | ------ | ------------------------------------------------------------------------------------------------------ |
| `POSTGRES_PORT`      | `5432`  | no        | server | Host port mapped to the Postgres container.                                                            |
| `NATS_PORT`          | `4222`  | no        | server | Host port mapped to the NATS client port.                                                              |
| `NATS_MONITOR_PORT`  | `8222`  | no        | server | Host port for the NATS monitoring endpoint. Internal — do not expose publicly.                         |
| `MINIO_PORT`         | `9000`  | no        | server | Host port mapped to the MinIO S3 API.                                                                  |
| `MINIO_CONSOLE_PORT` | `9001`  | no        | server | Host port for the MinIO web console. Admin surface — do not expose publicly.                           |
| `ATLAS_HTTP_PORT`    | `8080`  | no        | server | Host port mapped to the atlas HTTP API.                                                                |
| `ATLAS_GRPC_PORT`    | `50051` | no        | server | Host port mapped to the atlas gRPC API (Evidence SDK push). Internal — gate behind a proxy if exposed. |
| `WEB_PORT`           | `3000`  | no        | web    | Host port mapped to the Next.js frontend.                                                              |

## Tenancy and bootstrap

| Variable                         | Default                                | Required? | Scope     | Description                                                                                                                                                                                                                                                                                                                   |
| -------------------------------- | -------------------------------------- | --------- | --------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `ATLAS_BOOTSTRAP_TENANT`         | `00000000-0000-4000-8000-000000000001` | no        | bootstrap | UUID of the default tenant the bootstrap container seeds. The solo operator is a single-tenant deployment of the multi-tenant system. Leave the default unless migrating data.                                                                                                                                                |
| `ATLAS_OAUTH_TOKEN_RATE_PER_MIN` | `600`                                  | no        | server    | OAuth token-endpoint rate limit, per `client_id`, per minute (slice 187). Raised above the 60/min library default so the bootstrap container's idempotent control-bundle re-runs (up to ~100 token acquisitions/min) do not exhaust the budget. The envelope is per-`client_id`; other clients are unaffected. Tune to taste. |

## Related references

- [Install — self-host quickstart](install.md) — the one-command
  bring-up that consumes these variables.
- [`docs/SELF_HOSTING.md`](https://github.com/mgoodric/security-atlas/blob/main/docs/SELF_HOSTING.md)
  — the deeper deploy guide.
- [`deploy/docker/.env.example`](https://github.com/mgoodric/security-atlas/blob/main/deploy/docker/.env.example)
  — the editable template this page documents.
