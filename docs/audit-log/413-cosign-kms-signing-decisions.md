# Slice 413 decisions log — cosign-kms OSCAL bundle signing (368 Phase 1)

JUDGMENT slice. Build-time design calls made by the implementing agent
per the ADR-0010 ADOPT-DEFERRED plan. Does not block merge.

- detection_tier_actual: integration
- detection_tier_target: integration

(A real cosign-v3 behavior bug surfaced DURING the slice and was caught at
the integration tier — the cheapest tier that could have caught it, since
it is a property of the external binary, not of our Go code. See Decision
D3. No production-tier escape.)

---

## Decisions made

### D1 — Wrapper design: injectable exec boundary + typed errors

**Options:** (a) call `os/exec` directly inside `Client.SignBlob`;
(b) inject a `runner` interface so the cosign binary is a swappable
dependency.

**Chosen: (b).** `internal/oscal/cosign` defines an unexported
`runner interface { run(ctx, bin, env, stdin, args...) (out, err, error) }`;
production wires `execRunner` (a thin `os/exec` shim), tests inject a
fake. This makes every `Client` branch — argv construction, env
allowlist, error mapping, empty-output guard — unit-testable **with no
cosign binary**, while the real-binary path is exercised by the
integration tier. Mirrors the project's existing test-seam pattern
(slice 410/411 narrow interfaces; the bridge fake in
`internal/oscal/export_test.go`). cosign exit codes / absence map to
typed errors (`ErrCosignNotFound`, `ErrSignFailed`, `ErrVerifyFailed`,
`ErrTimeout`, `ErrBadConfig`) so callers branch with `errors.Is` rather
than parsing stderr. **Confidence: high.**

### D2 — Env allowlist: deny-by-default, static + extensible

**Options:** (a) inherit the caller's full env into the cosign
subprocess; (b) forward nothing; (c) a curated allowlist.

**Chosen: (c).** The cosign subprocess receives ONLY `PATH`, `HOME`, and
the cloud-KMS credential families (`AWS_*`, `GOOGLE_*`/`CLOUDSDK_*`,
`AZURE_*`, `VAULT_*`) — read from the process env at call time and
forwarded only when set. The atlas process's database DSN, OAuth signing
keys, etc. are **withheld** (unit-tested: `TestBuildEnv_AllowlistOnly`
asserts a `DATABASE_URL_APP`/`OSCAL_SIGNING_KEY` value never reaches the
subprocess). `WithExtraEnvKeys` lets a deployment add a non-standard
credential variable by NAME without code edits to the static list (values
still read from env, never stored on the Client). This is the
P0-368-3-adjacent "don't leak secrets into the shelled-out binary"
posture; deny-by-default is the conservative default the parent 368 doc
calls for. **Confidence: high.**

### D3 — cosign-v3 offline flags (the bug caught during the slice)

**What I found:** cosign v3.0.6 (the pinned version) **deprecated**
`--tlog-upload` and `--output-signature`, and `--tlog-upload=false`
errors out when used with the default TUF signing-config. More
importantly, I empirically confirmed that signing with **only**
`--use-signing-config=false` (and no `--tlog-upload=false`) STILL uploads
a Rekor transparency-log entry — which would violate P0-413-1 (no Rekor).

**Chosen argv:** sign with **both** `--use-signing-config=false`
**and** `--tlog-upload=false`, blob on stdin, signature to stdout (no
`--output-signature`/`--bundle`). The combination is load-bearing:
`--use-signing-config=false` avoids fetching the TUF service config, and
`--tlog-upload=false` is what actually suppresses the tlog entry. The
integration test `TestIntegration_NoTlogEntry` asserts a signed `--bundle`
contains **no** `tlogEntries`, locking the P0-413-1 guarantee against a
future cosign-flag regression. Verify uses the classic
`--key … --signature <path> --insecure-ignore-tlog=true -` form. The
`--tlog-upload` deprecation warning prints to stderr and is harmless (it
does not fail the call). **Confidence: high** (empirically verified
against the real binary).

### D4 — KMS-test strategy: local-key stand-in, clearly marked

**Options:** (a) require a real cloud KMS in CI (impossible/expensive);
(b) a documented fake; (c) a locally-generated throwaway cosign key as
the KMS-ref stand-in.

**Chosen: (b)+(c) layered.** The dispatch + manifest + Mode + digest
logic (the production Go code) is fully exercised by a **fake cosign**
(`fakeCosign` in `sign_cosign_test.go`) with NO binary — round-trip,
tamper-before-cosign, bad-signature, malformed-field, backward-compat.
The **real cosign exec boundary** (argv, offline flags, env, stdin/stdout)
is exercised by integration tests using a **locally-generated throwaway
key pair** (`cosign generate-key-pair`, empty password — no real key
material, GitGuardian-safe) standing in for a cloud-KMS key. The local
`--key cosign.key` path drives the IDENTICAL `sign-blob`/`verify-blob`
code path cosign uses for an `awskms://` URI; only key resolution
differs. **What is stubbed is stated in the test file's package comment.**
A real cloud-KMS round-trip is the one thing not exercised in CI — and is
on the revisit list. **Confidence: high** for the code paths covered;
the residual KMS-credential-plumbing is a deploy-time concern, not a
code-logic concern.

### D5 — Backward-compat seam: empty Mode → embedded (P0-413-4)

`Signature.Mode` is `json:"mode,omitempty"`. `ResolveMode("")` returns
`ModeEmbeddedEd25519`. A pre-413 manifest (no `mode` key) therefore
dispatches to the in-process verifier exactly as before.
`TestBackwardCompat_OldManifestVerifies` simulates a pre-413 manifest by
marshaling a current one and **deleting the `mode` key**, then verifies it
through both `VerifyBundle` and `VerifyBundleWithCosign`. This is the
single backward-compat seam and it is directly tested. **Confidence: high.**

### D6 — Two verify entrypoints, fail-closed

`VerifyBundle(b)` (no cosign dependency) handles embedded fully and
returns `ErrCosignVerifierRequired` for a cosign-mode bundle — it
**fails closed** rather than silently passing a signature path it did not
check. `VerifyBundleWithCosign(ctx, b, verifier)` is the superset the CLI
`verify` uses: embedded in-process, cosign-kms via the verifier. Keeping
the original `VerifyBundle` signature intact preserves the existing
callers (handler, tests) unchanged (P0-413-3 — minimal public-API
widening). **Confidence: high.**

### D7 — Mode-selection config centralized in `signconfig.go`

`ResolveSigningConfig(lookup)` resolves the mode from
`ATLAS_OSCAL_SIGNING_MODE` / `ATLAS_COSIGN_KMS_REF` (a set ref infers
cosign-kms) / default embedded. It takes an `os.LookupEnv`-shaped lookup
so it is unit-testable with a fake env, and is the single resolver the
CLI, config-check, and (future) server wiring share. `cosign-keyless` is
**rejected** here (P0-413-1). The air-gap default (no env → embedded) is
asserted by `TestResolveSigningConfig_DefaultsToEmbedded` (P0-413-2).
**Confidence: high.**

### D8 — CLI shape: new `oscal` parent, `oscal-export` untouched

Added a new `oscal` cobra command with `sign | verify | config-check`
subcommands (`cmd/atlas-cli/cmd_oscal_sign.go`), registered alongside the
existing `oscal-export` command (left byte-for-byte unchanged). Avoided
restructuring `oscal-export` into the new parent to keep the diff small
and the existing command's behavior/flags stable. Output goes through
`cmd.OutOrStdout()` (not `fmt.Printf`) so it is captured + testable.
**Confidence: high.**

### D9 — Server wiring NOT switched in this slice

`cmd/atlas/main.go`'s `oscalSignerFromEnv()` (the server export path)
still constructs only the embedded `*Signer`. This slice ships the
cosign-kms **capability** (wrapper + signer + dispatch + CLI + config)
but does NOT flip the **server's** default export to consult
`ResolveSigningConfig`. Rationale: the server export path
(`Exporter.signer *Signer`) is a concrete type; threading a mode-aware
signer through `NewExporter` is a wider API change than the
Mode-discriminator + dispatch P0-413-3 bounds, and the operator-facing
value (sign/verify/config-check + stock-cosign verifiability) lands via
the CLI now. **This is the top revisit item** — see below.
**Confidence: medium.**

### D10 — Coverage floor for the new package; advisory roster untouched

Added a hard floor `internal/oscal/cosign: 88` (merged unit+integration
measures ~91.5% with cosign present in CI). Did NOT add the package to the
slice-350 `$security_critical_packages` advisory roster: that block's
`$how_to_extend_roster` explicitly gates expansion beyond the
auth-substrate + tenancy spine behind "explicit justification / round-2."
The hard 88 floor already enforces high coverage on this security-critical
code without expanding a roster whose governance says not to. Installed
cosign in the `tests-integration` CI job (same `cosign-installer` +
v3.0.6 as release signing) so the integration tests actually run and the
floor is honest; enrolled `./internal/oscal/cosign/...` in the integration
package list (slice-345 guard passes: 88 tagged / 88 enrolled).
**Confidence: high.**

---

## Revisit once in use

1. **(D9, medium) Switch the server export path to mode-aware signing.**
   Today only the CLI honors `cosign-kms`; the server's
   `Exporter`/`oscalSignerFromEnv` still always embeds ed25519. The
   follow-on threads `ResolveSigningConfig` through `NewExporter` (a
   `BundleSigner` interface holding either `*Signer` or `*KMSSigner`) so
   an HTTP/API-triggered export in a KMS-configured deployment produces a
   `cosign-kms` bundle. File as a spillover slice when the connected-SaaS
   export path is exercised against a real KMS.
2. **(D4) Exercise a real cloud-KMS round-trip.** CI uses a local-key
   stand-in; the AWS/GCP/Azure credential-plumbing + IAM `kms:Sign` path
   is not exercised in automation. Re-check `config-check --probe` and a
   full sign→`cosign verify-blob` against a live KMS the first time a
   connected operator configures one.
3. **(D3) cosign flag drift.** The offline-no-tlog argv depends on
   cosign-v3 flag semantics (`--use-signing-config=false` +
   `--tlog-upload=false`). When the pinned cosign version bumps (a
   separate maintenance slice, per 368 notes), re-run
   `TestIntegration_NoTlogEntry` to confirm no tlog regression and check
   whether `--tlog-upload` was fully removed (drop it if so).
4. **(D2) Env allowlist completeness.** If a connected operator's KMS
   provider needs a credential variable not on the static allowlist (e.g.
   a newer AWS SSO or GCP workload-identity var), they hit a confusing
   "not usable" probe. Watch for this and extend `envAllowlist`.
5. **Asset-inventory provenance.** §2.3 + §3.3 now name the bundled
   cosign binary (v3.0.6, Apache-2.0). When 414 lands keyless or the
   cosign version bumps, update those rows.

---

## Confidence summary

| Decision                               | Confidence |
| -------------------------------------- | ---------- |
| D1 wrapper injectable exec boundary    | high       |
| D2 env allowlist deny-by-default       | high       |
| D3 cosign-v3 offline-no-tlog flags     | high       |
| D4 KMS-test local-key stand-in         | high       |
| D5 backward-compat empty-Mode seam     | high       |
| D6 two verify entrypoints, fail-closed | high       |
| D7 centralized mode resolution         | high       |
| D8 CLI shape (new oscal parent)        | high       |
| D9 server wiring deferred              | medium     |
| D10 coverage floor + CI cosign install | high       |
