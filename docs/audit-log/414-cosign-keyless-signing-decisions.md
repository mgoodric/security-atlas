# Slice 414 decisions log — cosign-keyless OSCAL bundle signing (368 Phase 2)

JUDGMENT slice. Build-time design calls made by the implementing agent per
the [ADR-0016](../adr/0016-oidc-identity-for-keyless-signing.md) ADOPT-DEFERRED
plan (which resolves [ADR-0010](../adr/0010-oscal-cosign-signing.md)'s deferred
OIDC-identity question). Does not block merge.

- detection_tier_actual: none
- detection_tier_target: none

(No bug surfaced during the slice. The keyless flow is exercised end-to-end
through faked Fulcio/Rekor at the cosign-client boundary — unit + an
integration on-disk round-trip — and the build/lint/coverage parity ran
clean. The one class of escape this design CANNOT catch in CI is a real
cosign-keyless argv/behavior drift against a live private Sigstore; that is
named in "Revisit once in use" and is structurally a v3 horizon, not a v1
gate — consistent with ADR-0016 horizoning the transparency-log leg to v3.)

---

## Scope this slice implements

Per ADR-0016, keyless ships as an **OPT-IN mode for the private-Sigstore
deployment shape** using atlas's scoped `client:oscal-signer` identity
(slice 188) federated into an **operator-run private Sigstore** (Fulcio +
Rekor, operator-controlled trust root) — NOT public Fulcio (options a/d
rejected), and NOT a default flip. The air-gap docker-compose default stays
`embedded-ed25519` (P0-414-2); the connected GA default stays `cosign-kms`
(P0-414-3).

---

## Decisions made

### D1 — Library choice: cosign-as-binary, NOT sigstore-go-as-library

**Options:** (a) add `github.com/sigstore/sigstore-go` (or cosign's Go
packages) and drive Fulcio cert-issuance + Rekor upload in-process;
(b) shell out to the `cosign` binary with keyless flags
(`--fulcio-url` / `--rekor-url` / `--identity-token`), mirroring the
Phase-1 kms path.

**Chosen: (b).** The entire existing signing surface
(`internal/oscal/cosign`) is a conservative binary-shellout wrapper with
**zero** Go sigstore dependencies — the kms mode shells out to
`cosign sign-blob --key`. Keyless reuses the same `runner` exec seam,
env-allowlist, timeout, and typed-error machinery; it just emits a
different (fixed) argv. `go.mod`/`go.sum` are **unchanged** — no new
dependency tree.

**Rationale:** consistency + minimal-deps. Adding sigstore-go would pull a
large, fast-moving dependency graph (its own Fulcio/Rekor/TUF clients,
protobuf bundles, x509 helpers) into the build for a single opt-in mode,
while the kms mode right beside it proves the binary approach is
sufficient and stock-`cosign verify-blob`-compatible (the auditor-friction
win ADR-0010 sells). The cosign binary is already bundled
license-clean (ADR-0010 cost ledger; Apache-2.0). One wrapper style, one
test seam, one bundled dependency.

**Confidence: high.** It is the established project pattern; the only cost
is that real-Fulcio behavior is not exercised in-process (covered by the
integration-horizon note below, same as kms).

### D2 — OIDC identity plumbing: an injected `IdentityTokenSource` interface

**Options:** (a) have the oscal package import the slice-188 oauth
internals and mint the `client:oscal-signer` token itself; (b) define a
narrow `IdentityTokenSource` interface in the oscal package and inject a
concrete source at the call site (server: slice-188-backed; CLI:
env-var-backed).

**Chosen: (b).** `oscal.IdentityTokenSource { IdentityToken(ctx) (string, error) }`.
The `KeylessSigner` depends only on this interface; it never imports
`internal/api/oauth` or `internal/auth`. The CLI wires an
`envIdentityTokenSource` (reads `ATLAS_COSIGN_IDENTITY_TOKEN` — the
operator obtains atlas's oscal-signer token out of band); the server will
wire a slice-188-backed source when the server-side export path adopts
keyless.

**Rationale:** keeps the oscal package decoupled and unit-testable (a fake
token source exercises the token-error / empty-token branches with no AS),
exactly the seam pattern the kms `CosignSigner` interface uses. Per
ADR-0016 the identity is `sub = client:oscal-signer`, `iss =` this
deployment's atlas AS issuer — the interface carries the _token_, and the
identity/issuer are read back from the _issued cert_ (see D3), so the
signer records the ground-truth identity, not a configured guess.

**Confidence: high.**

### D3 — Manifest extension shape: a `keyless` sub-object with cert + Rekor index + endpoints

**Options:** (a) flatten new fields (certificate, rekor index, identity,
issuer, fulcio/rekor URLs) directly onto `Signature`; (b) nest them in a
single `*KeylessAttestation` sub-object that is `omitempty`.

**Chosen: (b).** A nested, `omitempty` `Signature.Keyless` field of type
`*KeylessAttestation`. The attestation records: the PEM `certificate`; the
`certificate_identity` (the URI SAN, parsed from the issued cert); the
`certificate_oidc_issuer` (parsed from the Fulcio issuer X.509 extension OID
`1.3.6.1.4.1.57264.1.8`, with the legacy `…1.1` extension as fallback); the
`rekor_log_index`; and the `fulcio_url` / `rekor_url` the sign used.

**Rationale:**

- `omitempty` + nesting means a non-keyless `Signature` (embedded / kms /
  pre-414) marshals **byte-identical** to before — the backward-compat
  guarantee (asserted by `TestKeylessManifest_OmittedForOtherModes`).
- The attestation is **self-describing**: an auditor verifying a keyless
  bundle elsewhere gets the cert, the expected identity+issuer, and the
  private Rekor URL from the manifest itself — enough to run
  `cosign verify-blob --certificate … --certificate-identity …
--certificate-oidc-issuer … --rekor-url …`.
- The identity+issuer are **parsed from the issued cert**, not taken from
  config — the manifest records what Fulcio actually certified.
- `rekor_log_index` defaults to `-1` ("not captured") when cosign's stderr
  carries no `tlog entry created with index:` line; a missing index is not
  fatal to the sign (the cert + signature are the primary artifacts).

**Confidence: high** on the shape; **medium** on the exact issuer-extension
OID parsing surviving a future Fulcio cert-profile change (see Revisit).

### D4 — Config surface: keyless is opt-in and NEVER inferred

**Options:** (a) infer keyless when Fulcio/Rekor URLs are present (mirrors
how a set KMS ref infers kms); (b) require an explicit
`ATLAS_OSCAL_SIGNING_MODE=cosign-keyless` AND both private endpoints.

**Chosen: (b).** `ResolveSigningConfig` selects keyless ONLY on explicit
mode selection plus non-empty `ATLAS_COSIGN_FULCIO_URL` +
`ATLAS_COSIGN_REKOR_URL`. A configured Fulcio/Rekor with no explicit mode
leaves the default **embedded** (asserted by
`TestResolveSigningConfig_KeylessIsNeverInferred`).

**Rationale:** this is the load-bearing P0-414-2 / P0-414-3 guard. Keyless
must never become a deployment's default by accident — inferring it from
endpoint presence is exactly the silent-default-flip the P0s forbid. KMS
infers because kms is a safe connected default; keyless is the
heavy, opt-in, private-Sigstore mode and must be deliberately chosen.

**Confidence: high.**

### D5 — Verify dispatch: type-assert the keyless surface, fail closed

**Options:** (a) change `VerifyBundleWithCosign`'s parameter type;
(b) keep its `CosignVerifier` parameter and type-assert to
`CosignKeylessVerifier` inside the keyless case.

**Chosen: (b).** A verifier that does not also satisfy
`CosignKeylessVerifier` yields `ErrCosignVerifierRequired` (fail closed) —
the production `cosign.Client` (via the CLI's combined verifier) satisfies
both. This preserves the existing kms-callers' signature and keeps the
fail-closed property: a verifier that cannot actually check a keyless
bundle never reports it valid.

**Confidence: high.**

---

## Revisit once in use

- **Real private-Sigstore behavior.** The keyless argv/flag set
  (`--fulcio-url`, `--rekor-url`, `--identity-token`, `--output-certificate`,
  `--tlog-upload=true`) is faked at the client boundary in CI. Re-validate
  it against a real operator-run Fulcio+Rekor stack (e.g. sigstore-scaffolding
  in a dedicated, non-CI environment) before recommending keyless to an
  operator. This is the v3-horizon validation, consistent with ADR-0016.
- **Fulcio cert profile / issuer-extension OID.** `certIdentityAndIssuer`
  parses the SAN URI + the `57264.1.8` issuer extension. Re-check against
  the operator's Fulcio cert profile — a private Fulcio may emit the
  identity as an email SAN or use a different issuer-claim encoding.
- **Server-side keyless wiring.** This slice wires the CLI sign/verify
  path and the signer/config/manifest foundation. The server export path
  (`internal/api/oscalexport`) adopts keyless when a slice-188-backed
  `IdentityTokenSource` is wired in — file that as the follow-on when a
  connected deployment actually wants keyless.
- **Rekor inclusion-proof depth.** v1 records the Rekor log index and
  relies on `cosign verify-blob`'s built-in inclusion check at verify time.
  A deeper offline inclusion-proof persisted in the manifest is a possible
  future hardening.
- **Asset inventory.** When keyless is actually enabled by an operator,
  record the operator-Sigstore dependency in
  `docs/governance/asset-inventory.md` alongside the existing cosign-binary
  entry.

---

## Confidence summary

| Decision                                    | Confidence                               |
| ------------------------------------------- | ---------------------------------------- |
| D1 cosign-as-binary                         | high                                     |
| D2 IdentityTokenSource interface            | high                                     |
| D3 manifest `keyless` sub-object            | high (shape) / medium (issuer-OID parse) |
| D4 opt-in, never inferred                   | high                                     |
| D5 type-assert verify dispatch, fail closed | high                                     |
