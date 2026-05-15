# Troubleshooting: First-time login

A first-time user installing the docker-compose self-host bundle (slice 037) or the Helm chart (slice 038) lands on `/login` and needs the bootstrap admin bearer token. This page walks through every common failure mode.

The bootstrap admin token is generated at platform startup. You can find it via three orthogonal paths:

| Path | Command |
| --- | --- |
| Container logs (docker-compose) | `docker compose logs atlas 2>&1 \| grep BOOTSTRAP_TOKEN` |
| Container logs (Helm / kubectl) | `kubectl logs deploy/atlas --tail=200 2>&1 \| grep BOOTSTRAP_TOKEN` |
| Filesystem | `cat ${ATLAS_DATA_DIR:-/var/lib/atlas}/bootstrap-token` |

The token is **single-use**: on the first successful sign-in the platform atomically deletes the file AND marks `platform_status.first_signin_at` so the login page swaps back to the existing copy. Long-lived bootstrap tokens on disk are a credential leak shape this platform does not introduce.

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

Use the recovery flag on `atlas-cli credentials issue`:

**Verified on docker-compose:**

```sh
# From inside the atlas-cli container (sees the same network):
atlas-cli credentials issue \
  --tenant "$ATLAS_BOOTSTRAP_TENANT" \
  --reset-bootstrap --force \
  --endpoint atlas:50051 \
  --http-endpoint http://atlas:8080 \
  --token "$ATLAS_BOOTSTRAP_TOKEN"
```

The `--force` flag is the foot-gun gate: without it the CLI refuses because re-issuing bootstrap after a real user has signed in is rarely the right move. With it, the platform:

1. Mints a fresh admin bearer.
2. Clears `platform_status.bootstrap_token_consumed_at` and `first_signin_at`.
3. Writes the new token to `${ATLAS_DATA_DIR}/bootstrap-token` (mode 0600).
4. Returns the new bearer plaintext to stdout.

**Verified on Helm:**

```sh
kubectl exec -it deploy/atlas -- \
  atlas-cli credentials issue \
  --tenant "$ATLAS_BOOTSTRAP_TENANT" \
  --reset-bootstrap --force \
  --endpoint atlas:50051 \
  --http-endpoint http://atlas:8080 \
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

The platform is intentionally single-tenant for the v1 self-host operator (canvas §5.4 + OQ #13 resolution). The bootstrap admin credential is bound to `ATLAS_BOOTSTRAP_TENANT` — the single seeded tenant. To verify:

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

Expect exactly one row. If you see more than one, an admin operator has provisioned additional tenants; the bootstrap admin credential remains bound to `ATLAS_BOOTSTRAP_TENANT`. Use that tenant ID with the recovery `--reset-bootstrap --force` command if needed.

---

## See also

- [Install guide](../install.md) — full bring-up steps including where to find the bootstrap token on first run.
- [First audit](../first-audit.md) — what to do once the first admin is signed in.
