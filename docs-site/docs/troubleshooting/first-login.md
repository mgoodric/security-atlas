# Troubleshooting: First-time login

A first-time user installing the docker-compose self-host bundle (slice 037) or the Helm chart (slice 038) lands on `/login`. On a single-tenant install (the default) the page asks only for an email and password: sign in with `ATLAS_DEFAULT_USER_EMAIL` / `ATLAS_DEFAULT_USER_PASSWORD`, with no tenant ID. The email/password form stays available after the first sign-in. The bootstrap admin bearer token is the fallback when that account is not usable. This page walks through every common failure mode.

The bootstrap admin token is generated at platform startup. You can find it via three orthogonal paths:

| Path                            | Command                                                             |
| ------------------------------- | ------------------------------------------------------------------- |
| Container logs (docker-compose) | `docker compose logs atlas 2>&1 \| grep BOOTSTRAP_TOKEN`            |
| Container logs (Helm / kubectl) | `kubectl logs deploy/atlas --tail=200 2>&1 \| grep BOOTSTRAP_TOKEN` |
| Filesystem                      | `cat ${ATLAS_DATA_DIR:-/var/lib/atlas}/bootstrap-token`             |

The token is **single-use**: on the first successful sign-in the platform atomically deletes the file AND marks `platform_status.first_signin_at` so the login page swaps back to the existing copy. Long-lived bootstrap tokens on disk are a credential leak shape this platform does not introduce.

Paste the token into the **Developer sign-in** card on `/login`. That card is offered only while the install is in first-install state, which means no one has signed in yet, or a `--reset-bootstrap` (below) has put the install back into it. After the first sign-in, `/login` shows only the email and password form, and the web server refuses a pasted token. Set `ATLAS_WEB_DEV_TOKEN_SIGNIN=true` on the `web` service to keep the card available afterwards. See the [configuration reference](../configuration.md#web). If the web server cannot read the install state, it hides the card and refuses token sign-in.

---

## Token has scrolled out of the log buffer

The container's stdout has rolled, but the token is still on disk if no one has yet signed in.

**Verified on docker-compose:**

```sh
docker compose exec atlas cat /var/lib/atlas/bootstrap-token
# or on the host (the volume is bind-mounted by default):
cat ./atlas-data/bootstrap-token
```

**Verified on Helm:**

```sh
kubectl exec -it deploy/atlas -- cat /var/lib/atlas/bootstrap-token
```

If the file is also gone (someone signed in already) but you have no admin session, follow [Token was already consumed but no session was established](#token-was-already-consumed-but-no-session-was-established).

---

## Token was already consumed but no session was established

The file was deleted (someone called `/v1/install/mark-first-signin` — typically a successful sign-in) but the session cookie never made it back to the browser. `platform_status.first_signin_at` is set; a re-issued bootstrap token cannot be consumed without explicit intent.

Use the recovery flag on `atlas-cli credentials issue`, with the bootstrap token as `--token`. An admin API key such as `ATLAS_BOOTSTRAP_TOKEN` resets over gRPC (`--endpoint`), where API keys authenticate. The HTTP API accepts only JWTs, so `--http-endpoint` is used only when `--token` is a super_admin JWT. The atlas gRPC port serves plaintext inside the deployment network, hence `--insecure`.

**On docker-compose:**

```sh
# From a container on the atlas network:
atlas-cli credentials issue \
  --reset-bootstrap --force \
  --endpoint atlas:50051 --insecure \
  --token "$ATLAS_BOOTSTRAP_TOKEN"
```

The `--force` flag is the foot-gun gate: without it the CLI refuses because re-issuing bootstrap after a real user has signed in is rarely the right move. With it, the platform:

1. Clears `platform_status.bootstrap_token_consumed_at` and `first_signin_at`.
2. Mints a fresh admin bearer for the tenant of the `--token` credential (your organisation, on a single-tenant install). `--tenant <tenant uuid>`, if you pass it, must be that same tenant. Signing in with the new bearer gives an admin session.
3. Writes the new token to `${ATLAS_DATA_DIR}/bootstrap-token` (mode 0600).
4. Revokes any recovery key an earlier reset minted.
5. Returns the new bearer plaintext, its `credential_id` and its `expires_at` to stdout.
6. Logs the reset (`reset-bootstrap minted recovery admin credential`) with the new credential id, the caller's credential id and the transport, never the bearer.

`--token` must be an admin API key on an install with one tenant, or a super_admin JWT (with `--http-endpoint http://atlas:8080`) on any install. On an install with more than one tenant an admin API key is refused. `--scope` and `--kinds` are refused with `--reset-bootstrap`.

**The recovery key expires.** It lasts 24h by default. Pass `--ttl` to change that, up to `168h` (7 days). The key cannot be made non-expiring.

**Revoke it after you sign in.** If the first sign-in uses another credential, the platform revokes the recovery key itself. If you sign in with the recovery key, it stays live until it expires, because it is your session. Once you can sign in another way (local password or SSO), revoke it:

```sh
atlas-cli credentials revoke --id <credential_id> --endpoint atlas:50051 --insecure --token <your admin token>
```

**On Helm:**

```sh
kubectl exec -it deploy/atlas -- \
  atlas-cli credentials issue \
  --reset-bootstrap --force \
  --endpoint atlas:50051 --insecure \
  --token "$ATLAS_BOOTSTRAP_TOKEN"
```

---

## The platform is up but `/login` returns 500 in fresh-install mode

The `platform_status` singleton row is missing. The migration seeds it; if it has been deleted manually, re-create it. The row is platform-wide metadata — it has no `tenant_id`.

**Verified on docker-compose:**

```sh
docker compose exec postgres psql -U atlas_migrate atlas \
  -c "INSERT INTO platform_status (singleton_lock) VALUES (TRUE) ON CONFLICT DO NOTHING;"
```

**Verified on Helm:**

```sh
kubectl exec -it deploy/postgres -- psql -U atlas_migrate atlas \
  -c "INSERT INTO platform_status (singleton_lock) VALUES (TRUE) ON CONFLICT DO NOTHING;"
```

The CHECK constraint guarantees only one row can exist; the `ON CONFLICT DO NOTHING` makes the recovery idempotent.

---

## The bootstrap-token file shows `0600` but a different user owns it

The docker-compose bundle runs atlas-bootstrap as the container's root user (mapped to a non-root user on the host via `user:` in the compose file). The bootstrap-token file is written by the container; verify the host-side permissions match:

**Verified on docker-compose:**

```sh
ls -l ./atlas-data/bootstrap-token
# Expected: -rw------- 1 <UID> <GID> ... bootstrap-token
```

If `<UID>` is not your user, `sudo cat ./atlas-data/bootstrap-token` (or fix the compose `user:` mapping).

**Verified on Helm:**

Pods run as the non-root user configured in `deploy/helm/atlas/values.yaml`. The file inherits that ownership. `kubectl exec` runs as the pod's user, so `kubectl exec -it deploy/atlas -- cat ...` works without privilege escalation.

---

## I'm running a multi-tenant install and I don't know which tenant the bootstrap admin is in

A single-tenant install is the default (see [ADR 0023](https://github.com/mgoodric/security-atlas/blob/main/docs/adr/0023-single-tenant-experience-by-default.md)), and on one nothing on this page needs a tenant ID. An install becomes multi-tenant only when a super-admin creates a second tenant with `ATLAS_ALLOW_MULTI_TENANT=true` set; see [Tenant membership](../tenant-membership.md#opting-in-to-multi-tenant). The bootstrap admin stays in the bootstrap tenant, `ATLAS_BOOTSTRAP_TENANT`. To list the tenants:

**Verified on docker-compose:**

```sh
docker compose exec postgres psql -U atlas_migrate atlas \
  -c "SELECT id, name FROM tenants;"
```

**Verified on Helm:**

```sh
kubectl exec -it deploy/postgres -- psql -U atlas_migrate atlas \
  -c "SELECT id, name FROM tenants;"
```

One row means a single-tenant install. With several, pass the bootstrap tenant's ID as `--tenant` to the recovery `--reset-bootstrap --force` command if you need to name it.

---

## See also

- [Install guide](../install.md) — full bring-up steps including where to find the bootstrap token on first run.
- [First audit](../first-audit.md) — what to do once the first admin is signed in.
