# Slice 425 decisions log — LocalStack KMS round-trip integration test

AFK slice (add an opt-in integration test; no production-code change). The
calls below are build-time design choices made by the implementing agent
per the ADR-0010 Phase-1 "revisit" this slice closes. Does not block merge.

- detection_tier_actual: integration
- detection_tier_target: integration

(The slice is itself an integration-tier test. The one real behavior
finding it surfaced — the cosign-v3 awskms `GetPublicKey` credential
resolution being environment-shaped — was caught at the integration tier,
the only tier that exercises the external binary against a KMS endpoint.
No production-tier escape; nothing shipped to a deployed instance.)

---

## Decisions made

### D1 — Drive the REAL `Client.SignBlob`/`VerifyBlob`, not the argv mirror

The slice-413 stand-in (`signWithKeyFile` in `cosign_integration_test.go`)
builds the `sign-blob` argv by hand against a local `cosign.key`, which
**cannot** pass `validateKMSRef` (a local path is not a KMS URI) — so it
bypasses `Client.SignBlob` entirely. To actually close the revisit, slice
425 calls `Client.SignBlob(ctx, "awskms:///alias/...", blob)` and
`Client.VerifyBlob(...)` — exercising `validateKMSRef` + `buildEnv` + the
production argv end to end against LocalStack. That is the load-bearing
distinction: the real provider path is now EXECUTED, not just mirrored.
**Confidence: high.**

### D2 — Forward the LocalStack endpoint via `WithExtraEnvKeys`, NOT a signblob.go change

cosign's awskms provider (and the aws SDK) redirect to a sandbox via
`AWS_ENDPOINT_URL` / `AWS_ENDPOINT_URL_KMS`. Those two vars are NOT on the
static `envAllowlist` in `cosign.go`. Rather than add them to the
production allowlist (which would be a signblob.go behavior change —
forbidden by P0-425-4), the test constructs the client with
`New(WithExtraEnvKeys("AWS_ENDPOINT_URL", "AWS_ENDPOINT_URL_KMS"))`. This
is the **designed** extension seam (slice 413 D2): a test/deployment adds a
non-standard credential/endpoint var by name without touching the static
list. No production code changed. **Confidence: high.**

### D3 — Empirical finding: cosign-v3 awskms `GetPublicKey` credential resolution is environment-shaped (NOT a signblob.go bug)

While proving the round-trip against a real LocalStack KMS, both the sign
and verify paths invoke cosign's awskms `GetPublicKey`. With the static
`AWS_ACCESS_KEY_ID`/`AWS_SECRET_ACCESS_KEY` + `AWS_REGION` +
`AWS_ENDPOINT_URL*` env all correctly set and **exported into the
subprocess**, both `sign-blob` and `verify-blob` resolve credentials and
the full round-trip (sign → verify-OK → tamper-rejected) passes. Earlier
apparent "sign OK / verify FAIL" results were a shell-quoting artifact in
manual probing (an unquoted multi-var string passed to a single `env`
invocation), **not** a cosign or signblob.go defect — reproduced clean
once the env was exported per-variable. The Go test (which sets the env via
`t.Setenv` + the `WithExtraEnvKeys` allowlist, never a shell string) drives
it correctly: `TestIntegration_LocalStackKMS_SignVerifyRoundTrip` and
`TestIntegration_LocalStackKMS_VerifyFailsOnTamper` both PASS against live
LocalStack. **No spillover filed** — there is no behavior bug in
`signblob.go`; the path works as designed. **Confidence: high** (empirically
verified end-to-end against `cosign v3.0.6` + `localstack/localstack:3.8`).

### D4 — Opt-in gate + leg-scoped CI wiring (P0-425-1)

The test is gated behind `ATLAS_COSIGN_KMS_LOCALSTACK=1` (+
`ATLAS_COSIGN_KMS_LOCALSTACK_ENDPOINT`, default `http://localhost:4566`) and
self-skips (never fails) when the gate, `cosign`, the `aws` CLI, or a
reachable LocalStack are absent. In CI, LocalStack is started via
`docker run` (NOT a `services:` block — the project's non-default-CMD
optional-service convention, matching MinIO/NATS) and the enabling env is
set **only on leg B3**, the shard that owns `./internal/oscal/cosign/...`
per `scripts/integration-shards.txt`. Every other leg — and any
environment without LocalStack — skips the test, so the single required
`Go · integration (Postgres RLS)` check stays green either way.
`localstack/localstack:3.8` (community) + `SERVICES=kms` needs no
LocalStack license (a newer `latest` build gated on a license locally;
pinning the community 3.x tag avoids that). **Confidence: high.**

### D5 — ECDSA P-256 SIGN_VERIFY key spec

The ephemeral LocalStack key is created `--key-spec ECC_NIST_P256
--key-usage SIGN_VERIFY` — the ECDSA spec cosign's awskms provider signs
blobs with. A fresh key + uniquely-suffixed alias per test keeps the cases
isolated; an alias collision on rerun is repointed rather than failed.
**Confidence: high.**

### D6 — No-leak assertion guarded against vacuity (AC-6)

A `capturingRunner` wraps `execRunner` and records every argv + stdout +
stderr the cosign subprocess emits; the assertion fails if the LocalStack
secret value (the unique dummy sentinel, not the generic `test` access key)
appears anywhere. The assertion also fails if the captured buffer is empty,
so a future cosign that stops emitting stderr cannot silently make the
no-leak check vacuous. **Confidence: high.**

---

## Confidence summary

| Decision                                          | Confidence |
| ------------------------------------------------- | ---------- |
| D1 drive the real provider path (not argv mirror) | high       |
| D2 endpoint via WithExtraEnvKeys (no code change) | high       |
| D3 GetPublicKey cred resolution — no bug          | high       |
| D4 opt-in gate + leg-B3-scoped CI wiring          | high       |
| D5 ECDSA P-256 SIGN_VERIFY key spec               | high       |
| D6 no-leak assertion guarded vs vacuity           | high       |
