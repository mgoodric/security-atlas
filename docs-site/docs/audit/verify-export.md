# Verify a signed OSCAL export

You have been handed a security-atlas OSCAL export bundle — a directory
containing `ssp.json`, `assessment-plan.json`, `assessment-results.json`,
`poam.json`, and a `manifest.json`. This page tells you how to confirm,
independently, that the bundle is **authentic** (signed by the operator's
key) and **untampered** (no member has been altered since it was signed).

You can do this two ways:

- **Stock `cosign`** — for bundles signed in `cosign-kms` mode. No
  security-atlas software required; you use the `cosign` binary you would
  use to verify any other signed artifact.
- **`atlas oscal verify`** — a single-command convenience path that works
  for either signing mode and recomputes everything for you.

Verification is entirely **offline and read-only**. Nothing you run here
contacts the operator's system or modifies the bundle.

!!! note "Scope of this page"

    This page covers the two signing modes that ship today:
    `embedded-ed25519` and `cosign-kms`. A keyless / transparency-log
    (Fulcio + Rekor) mode is **not** available, so do not expect a Rekor
    inclusion proof or a `--certificate-identity` flow — neither applies
    to a bundle produced by the current release.

## 1. Determine which mode the bundle used

Open `manifest.json` and read `signature.mode`:

```bash
cat manifest.json | jq .signature.mode
```

| `signature.mode`     | Verify with                                                  |
| -------------------- | ------------------------------------------------------------ |
| `"cosign-kms"`       | Stock `cosign verify-blob` (§2) or `atlas oscal verify` (§3) |
| `"embedded-ed25519"` | `atlas oscal verify` (§3)                                    |
| absent / `""`        | Treat as `embedded-ed25519` (a pre-2026 export)              |

The mode tells you which verification path to follow. If the field is
absent, the bundle predates the mode discriminator and is an
`embedded-ed25519` bundle — use §3.

## 2. Verify a `cosign-kms` bundle with stock cosign

This is the path that needs **no security-atlas tooling**. You verify the
bundle's signature with the stock [`cosign`](https://docs.sigstore.dev/)
binary against a public key the operator publishes.

### What you need

- The `cosign` binary (`brew install cosign`, or download a release from
  the sigstore project).
- The operator's **public key file** — ask the operator to publish it
  with `cosign public-key`, giving you a file such as `atlas-oscal.pub`.
  This is the key you trust; see [Trusting the key](#trusting-the-key)
  below. Do **not** trust a key that arrives only inside the bundle.

### Step 1 — recompute the bundle digest

The signature is over a **deterministic digest** of the bundle: the
sha256 over the sorted `filename:memberhash` lines of every member listed
in `manifest.json`. Recompute it with stock tools and confirm it matches
the digest the manifest records:

```bash
# Rebuild the signed digest from the manifest's member list.
jq -r '.members[] | "\(.filename):\(.sha256)"' manifest.json \
  | LC_ALL=C sort \
  | tr -d '\r' > lines.txt
# Append the trailing newline each line is hashed with, then sha256 it.
recomputed=$(sha256sum lines.txt | cut -d' ' -f1)

echo "recomputed: $recomputed"
echo "manifest:   $(jq -r .signature.digest manifest.json)"
```

The two values **must** match. (You may also re-hash each member file and
confirm its sha256 equals the `sha256` recorded for it in the manifest —
that is what makes the digest a tamper check: change any member's bytes
and the recomputed digest diverges.)

### Step 2 — verify the signature over that digest

Extract the signature from the manifest, write the **raw digest bytes** to
a file, and run `cosign verify-blob`:

```bash
# The signature, written to a file (cosign --signature takes a path).
jq -r .signature.signature manifest.json > bundle.sig
# The blob cosign verifies is the RAW digest bytes (hex-decoded).
jq -r .signature.digest manifest.json | xxd -r -p > bundle.digest

cosign verify-blob \
  --key atlas-oscal.pub \
  --signature bundle.sig \
  --insecure-ignore-tlog=true \
  bundle.digest
```

A successful run prints `Verified OK`. A tampered bundle fails: either the
recomputed digest in Step 1 no longer matches the manifest, or
`cosign verify-blob` rejects the signature with a non-zero exit.

!!! info "Why `--insecure-ignore-tlog=true`?"

    These bundles are signed against a cloud KMS key only — they are
    **never** uploaded to a Sigstore transparency log (Rekor). The flag
    name is cosign's; here it means "do not require a Rekor entry,"
    which is correct for this signing mode. It does **not** weaken the
    signature check itself — the signature over the bundle digest is
    still fully validated against the operator's public key.

!!! tip "Using the KMS reference directly"

    The manifest's `signature.key_ref` records the operator's KMS key
    reference (e.g. `awskms:///alias/<operator-key>`). If you have your
    own read access to that KMS key, you may pass `--key "<key_ref>"`
    instead of the exported `.pub` file. Most external auditors will not
    have KMS access, so the published public-key file is the usual path.

## 3. Verify with `atlas oscal verify` (either mode)

If the operator gives you the `atlas` CLI, a single command verifies a
bundle of **either** mode — it reads `signature.mode`, recomputes the
digest, and validates the signature for you:

```bash
atlas oscal verify <bundle-dir>
```

A successful run prints, for example:

```
OK: <bundle-dir> verifies (mode=embedded-ed25519)
```

For an `embedded-ed25519` bundle this is fully in-process: the manifest
carries the ed25519 public key and the signature over the digest, and
verification needs no external tool. For a `cosign-kms` bundle, `atlas
oscal verify` shells out to `cosign` and resolves the key from the
manifest's `key_ref`, so it requires `cosign` on `PATH` and read access to
that KMS key — if you lack KMS access, use the stock public-key path in §2
instead.

## What verification proves (and what it does not)

| Property                                       | Covered?                                        |
| ---------------------------------------------- | ----------------------------------------------- |
| No member file altered since signing           | Yes — the digest is over every member's hash    |
| Signed by the holder of the operator's key     | Yes — the signature validates against that key  |
| The key is genuinely the operator's            | Only if you obtained it out-of-band (see below) |
| An independent timestamp / non-repudiation log | No — these modes do not use a transparency log  |

## Trusting the key

A signature only means as much as your trust in the key that made it. For
the `cosign-kms` mode, obtain the operator's public key (or confirm the
`key_ref`) through a channel **independent of the bundle** — the
operator's published documentation, a key fingerprint confirmed
out-of-band, or your own access to the KMS key. A public key that arrives
only inside the same bundle proves nothing about who produced it: a
tamperer who could rewrite a member could also swap in their own key. The
operator establishes that out-of-band link; this page is the verifier's
half of it.

## Where to read more

- **Operator configuration** (how the operator sets up signing modes,
  KMS keys, and publishes the public key) lives in the operator runbook:
  [`docs/runbooks/oscal-signing.md`](https://github.com/mgoodric/security-atlas/blob/main/docs/runbooks/oscal-signing.md).
  That document is the operator's source of truth; this page is the
  auditor-facing derivative and does not repeat its configuration content.
- **How the bundle is produced** — see the
  [OSCAL SSP export walkthrough](../walkthroughs/oscal-ssp-export.md).
- **The signing design decision** — [ADR-0010](https://github.com/mgoodric/security-atlas/blob/main/docs/adr/0010-oscal-cosign-signing.md).
