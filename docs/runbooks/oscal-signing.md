# Runbook — OSCAL export-bundle signing modes

This runbook covers how security-atlas signs the OSCAL audit-export
bundle, how to choose a signing mode, how to configure each, and how an
auditor verifies a bundle. It documents **Phase 1** of the ADR-0010
cosign migration (slice 413 / 368a): the `embedded-ed25519` and
`cosign-kms` modes. The `cosign-keyless` mode (Fulcio + Rekor) is **not
yet available** — it is slice 414 (368b), gated on a separate
OIDC-identity decision.

> Design authority: [ADR-0010](../adr/0010-oscal-cosign-signing.md).
> Code: `internal/oscal/sign.go` (embedded); `internal/oscal/sign_cosign.go` +
> `internal/oscal/cosign/` (cosign-kms); `cmd/atlas-cli/cmd_oscal_sign.go` (CLI).

---

## What gets signed

When an operator or auditor exports a **frozen** AuditPeriod, atlas
assembles a bundle (`ssp.json`, `assessment-plan.json`,
`assessment-results.json`, `poam.json`, `manifest.json`) and signs a
**deterministic digest** of it:

- The digest is the sha256 over the sorted `filename:memberhash` lines of
  every member. It changes if **any** member's bytes change, so a
  post-export tamper is detected on verification.
- The signature is recorded in `manifest.json` under `signature`, which
  now carries a **`mode`** field (`embedded-ed25519` or `cosign-kms`) so
  a verifier knows which validation path to run.

The digest derivation is identical across modes — only **how the digest
is signed** differs.

---

## The two Phase-1 modes

| Mode               | Runtime dependency      | Network           | Identity anchor              | Air-gap | Auditor verifies with          |
| ------------------ | ----------------------- | ----------------- | ---------------------------- | ------- | ------------------------------ |
| `embedded-ed25519` | none (Go binary only)   | none              | raw public key in manifest   | YES     | `atlas oscal verify` (bespoke) |
| `cosign-kms`       | `cosign` binary + a KMS | KMS endpoint only | the KMS key + its IAM policy | no      | stock `cosign verify-blob`     |

Neither Phase-1 mode uses Fulcio, Rekor, or an OIDC identity. `cosign-kms`
signs **offline of the Sigstore public infrastructure** — it never
uploads to a transparency log.

---

## Choosing a mode (defaults per deployment)

Per the ADR-0010 default table:

| Deployment shape                         | Default            | Notes                                             |
| ---------------------------------------- | ------------------ | ------------------------------------------------- |
| Self-host, air-gapped (`docker-compose`) | `embedded-ed25519` | The only mode reachable with no network. Default. |
| Self-host, connected, with a cloud KMS   | `cosign-kms`       | Opt in by setting `ATLAS_COSIGN_KMS_REF`.         |
| Self-host, connected, no KMS             | `embedded-ed25519` | No cloud coupling is forced on you.               |
| SaaS / Helm                              | `cosign-kms` at GA | `cosign-keyless` only after slice 414.            |

**Air-gap guidance (read this):** `embedded-ed25519` is the default and
the only mode an air-gapped deployment can use. It is fully hermetic —
no external binary, no network. Do **not** switch an air-gapped
deployment to `cosign-kms`: a KMS is a network service and the export
will fail when it cannot reach the KMS endpoint. If you are unsure
whether your deployment can reach a KMS, stay on `embedded-ed25519`.

---

## Configuration

The mode is selected by environment variables, resolved identically by
the server, the CLI, and `config-check`:

| Variable                   | Purpose                                                                 |
| -------------------------- | ----------------------------------------------------------------------- |
| `ATLAS_OSCAL_SIGNING_MODE` | Explicit mode: `embedded-ed25519` or `cosign-kms`. If unset, see below. |
| `ATLAS_COSIGN_KMS_REF`     | The cosign KMS key reference. A set value **infers** `cosign-kms`.      |
| `ATLAS_COSIGN_BINARY`      | Override the cosign binary path (default: resolve `cosign` on PATH).    |
| `OSCAL_SIGNING_KEY`        | Hex ed25519 private key for `embedded-ed25519` (else an ephemeral key). |

**Resolution rules:**

1. If `ATLAS_OSCAL_SIGNING_MODE` is set, it is authoritative. `cosign-kms`
   then **requires** `ATLAS_COSIGN_KMS_REF`.
2. Else if `ATLAS_COSIGN_KMS_REF` is set, the mode is inferred as
   `cosign-kms`.
3. Else the mode is `embedded-ed25519` (the air-gap-safe default).

### `embedded-ed25519` setup

Nothing to install. For a stable identity across exports, set a
persistent key:

```bash
# Generate a 64-byte ed25519 private key, hex-encoded.
export OSCAL_SIGNING_KEY="$(openssl genpkey -algorithm ed25519 -outform DER \
  | tail -c 32 | xxd -p -c 64)"   # illustrative — keep this secret
```

Without `OSCAL_SIGNING_KEY`, atlas generates a fresh ephemeral keypair
per process; the public half travels in every manifest, so signatures
stay verifiable but are not anchored to a long-lived identity.

### `cosign-kms` setup

1. **Install cosign** (Apache-2.0; bundle-clean per ADR-0010). Pin v3.x
   (the project tracks v3.0.6). On a host: `brew install cosign` or the
   `sigstore/cosign-installer` action in CI.
2. **Create a KMS key** in your cloud and grant the atlas runtime
   identity `sign` + `verify` permission. cosign KMS references:
   - AWS: `awskms:///arn:aws:kms:<region>:<acct>:key/<id>` or `awskms:///alias/<name>`
   - GCP: `gcpkms://projects/<p>/locations/<l>/keyRings/<r>/cryptoKeys/<k>`
   - Azure: `azurekms://<vault>.vault.azure.net/keys/<key>`
   - Vault: `hashivault://<transit-key>`
3. **Configure atlas:**

   ```bash
   export ATLAS_OSCAL_SIGNING_MODE=cosign-kms
   export ATLAS_COSIGN_KMS_REF="awskms:///alias/atlas-oscal"
   # plus the cloud-SDK credential vars your provider needs (AWS_*, GOOGLE_*,
   # AZURE_*, VAULT_*) — see the env allowlist note below.
   ```

4. **Verify the config:**

   ```bash
   atlas oscal config-check --probe
   ```

   `--probe` runs a live test sign against the KMS key, proving
   credentials + key-use permission.

> **Credential env allowlist (security note).** The cosign subprocess
> does **not** inherit the atlas process's full environment. Only a
> curated allowlist is forwarded: `PATH`, `HOME`, and the cloud-KMS
> credential variables (`AWS_*`, `GOOGLE_*`/`CLOUDSDK_*`, `AZURE_*`,
> `VAULT_*`). Anything else — your database DSN, OAuth signing keys — is
> withheld from cosign. If your deployment uses a non-standard credential
> variable, it can be added to the allowlist in code
> (`internal/oscal/cosign/cosign.go`, `WithExtraEnvKeys`).

---

## CLI surface

```bash
# Report the resolved mode and whether prerequisites are met.
atlas oscal config-check            # quick check
atlas oscal config-check --probe    # cosign-kms: live test-sign the KMS key

# Sign an on-disk bundle directory with the configured mode.
atlas oscal sign <bundle-dir>

# Verify a bundle, dispatching on its recorded manifest mode.
atlas oscal verify <bundle-dir>
```

`oscal-export` (the full frozen-period export command) is unchanged.

---

## Auditor verification

### `cosign-kms` bundles — stock cosign

This is the value Phase 1 delivers: an auditor verifies with **stock
cosign**, no atlas-specific tooling. Recompute the bundle digest (sorted
`filename:memberhash` lines, sha256) — or use `atlas oscal verify` — then:

```bash
cosign verify-blob \
  --key "awskms:///alias/atlas-oscal" \   # the manifest's signature.key_ref
  --signature <signature-from-manifest> \
  --insecure-ignore-tlog=true \           # no Rekor entry by design (Phase 1)
  <digest-blob>
```

The operator can also publish the KMS public key (`cosign public-key
--key <kms-ref> > atlas-oscal.pub`) so verifiers use a plain key file.

### `embedded-ed25519` bundles

```bash
atlas oscal verify <bundle-dir>
```

The manifest carries the ed25519 public key and the signature over the
digest; verification is fully in-process.

---

## Migration / backward compatibility

A bundle signed by the **pre-slice-413** ed25519 path has **no `mode`
field** in its manifest. Verification still works: an absent/empty mode
**defaults to `embedded-ed25519`**, so every prior export verifies
exactly as before. No re-signing is required. (Tested:
`internal/oscal/sign_cosign_test.go`
`TestBackwardCompat_OldManifestVerifies`.)

---

## Failure modes & troubleshooting

| Symptom                                                | Cause / fix                                                                 |
| ------------------------------------------------------ | --------------------------------------------------------------------------- |
| `cosign binary not found`                              | Install cosign, set `ATLAS_COSIGN_BINARY`, or switch to `embedded-ed25519`. |
| `cosign-kms requires ATLAS_COSIGN_KMS_REF to be set`   | Set the KMS reference, or use `embedded-ed25519`.                           |
| `config-check --probe` → `KMS key … not usable`        | Cloud credentials / IAM: the runtime identity lacks `kms:Sign` on the key.  |
| `KMS reference … does not use a known provider scheme` | Use an `awskms://` / `gcpkms://` / `azurekms://` / `hashivault://` URI.     |
| `bundle digest mismatch — members changed`             | A member file was altered after signing. The bundle is not authentic.       |
| `cosign-keyless is not available`                      | Phase 1 ships kms + embedded only. Keyless is slice 414.                    |
