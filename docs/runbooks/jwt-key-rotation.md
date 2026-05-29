# Runbook — JWT signing key rotation

**Scope:** the OAuth Authorization Server's ES256 JWT signing keys (the keystore at `ATLAS_KEYSTORE_PATH`, default `/var/lib/security-atlas/keys/`). Slice 366 implements end-to-end rotation on top of the slice-187 keystore scaffolding.

**Audience:** the self-host operator (the solo security leader) and anyone responding to a suspected key compromise.

**Constitutional context:** a signing key is the platform's single point of trust for every JWT it mints. Long-lived static keys violate NIST SP 800-57 Part 1 §5.3.6 guidance and are a first-pass finding in any third-party security review (closes slice-327 audit finding M-1).

---

## Mental model

The keystore directory holds one PKCS#8 PEM file per keypair, named `<KeyID>.key` where `KeyID` is a chronological-lexicographic timestamp (`YYYYMMDDTHHMMSSZ`, mode 0600).

- **Active signing key** = the alphabetically-last (newest) KeyID. New JWTs are signed with it.
- **Verification keys** = every key in the directory. The JWKS endpoint (`/.well-known/jwks.json`) publishes all of them. A JWT presented by a client verifies if its `kid` header matches any published key.
- **Rotation** writes a new (newer-timestamp) key file; it immediately becomes the active signer. The prior key stays on disk as a verification-only key for the **overlap window**, so JWTs already issued under the old key keep verifying until they expire naturally.
- **Pruning** deletes key files past the overlap window. The active signer is **never** pruned (P0-366-2).

### Timing invariants (do not violate)

- **Access-token lifetime:** 1h (`internal/api/oauth.AccessTokenLifetime`).
- **Default overlap window:** 24h (`fsstore.DefaultRotationOverlap`) — 24× the access-token TTL, matching ADR-0003.
- **Prune cutoff:** `now − AccessTokenLifetime − overlap`. A key older than the cutoff is prunable. Because the cutoff always subtracts at least the full access-token lifetime, no JWT in flight is ever invalidated mid-validation (P0-366-3).
- **JWKS cache TTL:** 1h (`Cache-Control: max-age=3600`). During a 24h overlap window every verifier re-fetches JWKS ~24×, so it always sees both keys before the old one sunsets.
- **Rotation cadence (scheduled):** annual by default (`ATLAS_KEY_ROTATION_INTERVAL`, a Go duration string). Rotation is for forward security, not incident response — see emergency rotation below.

---

## Routine annual rotation (automatic)

The platform binary (`cmd/atlas`) starts a background scheduler when `ATLAS_ISSUER_URL` is set. Every `ATLAS_KEY_ROTATION_INTERVAL` (default 365d) it:

1. Generates a fresh ES256 keypair and makes it the active signer.
2. Writes one structured audit log line: `audit_event=key_rotation actor=scheduler previous_signing_kid=... new_signing_kid=... occurred_at=...`.
3. Prunes keys past the overlap window.

No operator action is required. To verify the scheduler is running, grep the startup logs for `atlas: JWT key rotation scheduler started`.

To shorten the cadence for a high-security deployment:

```sh
ATLAS_KEY_ROTATION_INTERVAL=2160h   # 90 days
```

---

## Manual rotation (operator-initiated)

Use the CLI. These commands operate directly on the keystore directory and need **no** database connection.

```sh
# Inspect current keys (role + age).
atlas-cli keys list --keystore /var/lib/security-atlas/keys
# 20260101T000000Z  role=signing    age=120d
# 20251001T000000Z  role=verifying  age=212d

# Rotate now.
atlas-cli keys rotate --keystore /var/lib/security-atlas/keys
# rotated: previous_signing_kid=20260101T000000Z new_signing_kid=20260529T101500Z

# Preview what a prune would remove (DRY RUN — removes nothing).
atlas-cli keys prune --keystore /var/lib/security-atlas/keys
# would-prune: 20251001T000000Z (age=240d)
# dry-run: 1 key(s) eligible for prune; re-run with --confirm to remove

# Actually prune.
atlas-cli keys prune --keystore /var/lib/security-atlas/keys --confirm
# pruned: 20251001T000000Z
# pruned 1 key(s)
```

`--keystore` is optional; without it the CLI resolves `ATLAS_KEYSTORE_PATH`, then the compiled-in default.

> The running server caches its keystore in memory and reloads on its own rotate/prune calls. If you rotate via the CLI against a **running** server's directory, the server will not pick up the new key until it next reloads (on its own scheduled rotation, or a restart). For an immediate cutover on a running server, prefer triggering rotation through the scheduler cadence or restart the server after a CLI rotate. (Hot-reload of CLI-driven rotations into the running process is a documented revisit item — see the slice-366 decisions log.)

---

## Emergency rotation (suspected compromise)

Rotation alone does **not** revoke existing tokens — tokens signed with the rotated-out key keep verifying until they expire (max 1h) or until the key is pruned. For a suspected compromise you must do BOTH:

1. **Rotate immediately** so all _new_ tokens use a fresh key:
   ```sh
   atlas-cli keys rotate
   ```
2. **Revoke outstanding tokens** so _existing_ tokens signed with the compromised key stop working before their natural expiry. Use the OAuth revocation endpoint (`POST /oauth/revoke`, RFC 7009, slice 190) to revoke specific JTIs, OR — if the entire key is suspect — **prune the compromised key out of the verification set** so every JWT bearing its `kid` is rejected:
   ```sh
   # Force the compromised key out of JWKS immediately. This rejects
   # EVERY token signed with it — including valid in-flight tokens — so
   # only do this under genuine compromise.
   atlas-cli keys prune --confirm --overlap 0
   ```
   With `--overlap 0` the cutoff becomes `now − 1h`, so any key older than the access-token lifetime is removed. The just-rotated active key is protected (P0-366-2).
3. Restart the server (or wait for its keystore reload) so the running process drops the compromised key from its in-memory verification set.
4. Confirm: `curl https://<issuer>/.well-known/jwks.json` no longer lists the compromised `kid`.

---

## Key recovery

The keystore is on-disk PKCS#8 PEM at mode 0600. Treat it with the same gravity as the database encryption key.

- **Backups:** include `ATLAS_KEYSTORE_PATH` in the same backup regime as the database. A restored keystore rehydrates every key file on boot (`fsstore.Open` rescans the directory).
- **Lost keystore, no backup:** the platform refuses to verify any previously-issued JWT (no matching `kid`). On next boot with an empty directory, `Open` generates a fresh keypair (P0-366-5 — the manual-regenerate fallback). Every existing session must re-authenticate. This is a hard outage for active sessions but not data loss.
- **Never** log, email, or paste key file contents. The audit and operator surfaces expose only KeyIDs (P0-366-1).

---

## JWKS publication impact

The JWKS endpoint (`/.well-known/jwks.json`) is unauthenticated (RFC 8414 §3) and publishes the full verification set with `Cache-Control: max-age=3600`. After any rotation:

- The new key appears in JWKS immediately (server reload) or on the next scheduled reload.
- The old key remains in JWKS until pruned.
- Downstream verifiers (the platform's own R2 middleware, plus any federated consumer) re-fetch JWKS within the 1h cache TTL and accept both keys.

A rotation followed immediately by a prune-with-`--overlap 0` is the only sequence that can reject in-flight tokens — reserve it for compromise.

---

## References

- ADR-0003 — OAuth Authorization Server § Key rotation strategy
- `docs/audit-log/366-jwt-key-rotation-decisions.md` — design decisions + revisit list
- NIST SP 800-57 Part 1 §5.3.6 — cryptoperiod guidance
- RFC 7009 — OAuth 2.0 Token Revocation (incident-response complement to rotation)
- Slice-327 security audit finding M-1
