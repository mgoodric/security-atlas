# 425 — Real cloud-KMS round-trip integration test for `internal/oscal/cosign`

**Cluster:** Quality
**Estimate:** 1-2d (M)
**Type:** AFK
**Status:** `ready`
**Priority:** P2

## Narrative

**WHY.** Slice 413 shipped the `cosign-kms` signing mode, but its
integration test
(`internal/oscal/cosign/cosign_integration_test.go`) uses a **local-key
stand-in** explicitly "in place of a cloud-KMS key reference" — it drives
the _identical_ `sign-blob` argv through `--key cosign.key`, but the
real `awskms://` / `gcpkms://` provider path in `signblob.go`
(`"--key", kmsRef` → `cosign sign-blob`) is asserted only by
argv-mirroring, never _executed_ against a real provider. Slice 413's
AC-6 round-trips only local + embedded modes, and the slice explicitly
flagged real-KMS as a "revisit." So the load-bearing claim of slice 413
— that an operator with a KMS gets a `cosign verify-blob`-verifiable
bundle — has no test that drives an actual KMS sign→verify.

**WHAT.** An opt-in integration test that exercises the real
`cosign sign-blob --key awskms://...` → `cosign verify-blob` round-trip
against **LocalStack KMS**, behind a build tag / env gate, with
LocalStack wired into the integration harness as an _optional_ dependency
(skip-with-note when absent, matching the project's other optional-dep
tests). This proves the `awskms://` argv path actually produces a
verifiable signature, not just the right command line.

**SCOPE DISCIPLINE.** One provider (`awskms://` via LocalStack) is enough
to prove the KMS argv path executes end-to-end; `gcpkms://` parity can be
a follow-on if demand surfaces. The test is **opt-in** — it must NOT
become a hard CI gate that blocks merges when LocalStack is unavailable
(it skips with a note, like the existing optional-service tests). No
change to `signblob.go` behavior — this is a test that exercises the
existing path; a bug it reveals is a spillover fix.

## Threat model

**S — Spoofing.** A KMS key reference identifies an operator-controlled
signing identity.

- Mitigation: the test uses an ephemeral LocalStack-created key; no real
  cloud identity is impersonated. The env gate selects LocalStack
  explicitly — the test never reaches a real cloud KMS by accident.

**T — Tampering.** The whole point of signing is tamper-evidence.

- Mitigation: the test signs a blob, then asserts `cosign verify-blob`
  succeeds for the unmodified blob AND fails for a mutated blob — proving
  the KMS-backed signature actually detects tampering (not just that
  sign returned 0).

**R — Repudiation.** Signing should be attributable.

- Mitigation: n/a at the test tier; the test proves the cryptographic
  binding the signing-mode manifest relies on.

**I — Information disclosure.** KMS access requires credentials; logs
must not leak them.

- Mitigation: the env gate provides LocalStack creds
  (`test`/`test`-class dummy values LocalStack accepts); the test asserts
  no credential material reaches stdout/stderr/log output. Fixtures carry
  no real key material (the existing test is GitGuardian-safe; this one
  stays so).

**D — Denial of service.** A hung `cosign` subprocess could stall CI.

- Mitigation: the cosign wrapper already enforces timeouts (slice 413
  AC-1); the test runs under those. The opt-in gate means an absent
  LocalStack skips fast, never hangs.

**E — Elevation of privilege (HEADLINE — signing-key handling).** A
mishandled KMS reference or leaked credential is a signing-key
compromise — the worst outcome for a signing subsystem.

- Mitigation: ephemeral LocalStack key, dummy creds via env gate, no real
  key material in fixtures, explicit assertion that the env gate does not
  leak creds to logs. The test exercises the path in a hermetic sandbox,
  never a production KMS.

**Verdict:** `has-mitigations`. Signing-key handling is the headline; the
env-gate + LocalStack hermeticity + no-leak assertion bound it.

## Acceptance criteria

- [ ] **AC-1 (test).** A new integration test (build-tagged
      `//go:build integration`, e.g.
      `cosign_kms_localstack_integration_test.go`) drives
      `cosign sign-blob --key awskms://...` against LocalStack KMS via the
      real `signblob.go` provider path.
- [ ] **AC-2 (test).** The test round-trips: it then runs
      `cosign verify-blob` against the LocalStack-backed key and asserts
      verification SUCCEEDS for the unmodified blob.
- [ ] **AC-3 (test).** The test asserts `cosign verify-blob` FAILS for a
      mutated blob (tamper-evidence proven, not assumed).
- [ ] **AC-4 (test).** The test is opt-in via an env gate (e.g.
      `ATLAS_COSIGN_KMS_LOCALSTACK=1` + a LocalStack endpoint env var);
      when the gate/endpoint is absent it `t.Skip`s with a clear note —
      it does NOT fail.
- [ ] **AC-5 (CI).** LocalStack is wired into the integration harness as
      an OPTIONAL dependency (compose service / docker-run startup step);
      when present, the gated test runs; when absent, it skips. The
      `Go · integration` job stays green either way.
- [ ] **AC-6 (test).** The test asserts no KMS credential material appears
      in captured stdout/stderr/log output (no-leak assertion).
- [ ] **AC-7.** Fixtures carry NO real key material and NO
      vendor-prefixed secrets — LocalStack dummy creds only (GitGuardian-
      safe).
- [ ] **AC-8.** The existing slice-413 local-key + embedded round-trip
      tests are left intact (the stand-in remains the always-on path; this
      adds the real-KMS path on top).
- [ ] **AC-9 (docs).** The cosign operator runbook (slice 413) gains a
      short note: how to run the LocalStack KMS round-trip locally + the
      env gate.

## Constitutional invariants honored

- **Evidence integrity — cosign signing of audit-export bundles
  (CLAUDE.md tech stack).** This test proves the KMS-backed signing path
  the bundle integrity relies on actually verifies.
- **Integration tier retry policy (slice 353 Q-16).** No `-retry` — a
  flake here is investigated to root cause (a hung cosign / LocalStack
  race is a real bring-up bug worth fixing once).
- **Integration enrolment policy (slice 353 Q-7).** The new tagged test
  is enrolled in the integration job's package list (it already imports
  the real provider path).

## Canvas references

- `Plans/canvas/09-tech-stack.md` — Evidence integrity (cosign signing).
- ADR-0010 (`docs/adr/0010-oscal-cosign-signing.md`) — the KMS mode this
  test exercises.
- Slice 413 decisions log
  (`docs/audit-log/413-cosign-kms-oscal-signing-phase-1-decisions.md`) —
  the real-KMS "revisit" this slice closes.

## Dependencies

- **#413** (cosign-kms Phase 1) — `merged`. The `signblob.go` `awskms://`
  path under test + the local-key stand-in this slice extends.

## Anti-criteria (P0 — block merge)

- **P0-425-1.** Does NOT make the LocalStack KMS test a hard CI gate —
  it MUST skip-with-note when LocalStack is absent (opt-in, like other
  optional-dep tests).
- **P0-425-2.** Does NOT reach a real cloud KMS — LocalStack only; the
  env gate selects the sandbox explicitly.
- **P0-425-3.** Does NOT put real key material or vendor-prefixed secrets
  in fixtures (LocalStack dummy creds only).
- **P0-425-4.** Does NOT change `signblob.go` behavior — it exercises the
  existing path; a revealed bug is a spillover fix slice.
- **P0-425-5.** Does NOT remove or weaken the slice-413 local-key
  stand-in tests (they stay the always-on path).
- **P0-425-6.** Does NOT modify `_STATUS.md` from inside this slice's own
  commits.

## Skill mix (3-5)

- `tdd` (round-trip integration test)
- `engineering-advanced-skills:env-secrets-manager` (env-gate + no-leak
  discipline)
- `Security` (STRIDE signing-key re-verification)
- `simplify` (pre-PR)

## Notes for the implementing agent

- Read `internal/oscal/cosign/cosign_integration_test.go` (the stand-in,
  clearly marked at the top) + `signblob.go` (the `--key kmsRef` argv at
  lines ~46-47 sign, ~111 verify) for the exact argv the LocalStack test
  must drive.
- LocalStack KMS speaks the AWS KMS API; cosign's `awskms://` URI is
  `awskms://<endpoint>/<key-id>` (or via `AWS_ENDPOINT_URL` env). Use the
  GHA service-container patterns the project already uses for
  optional-dep services (MEMORY: GHA service-container gotchas — prefer a
  docker-run startup step over `services:` for non-default-CMD images).
- The existing tests round-trip local + embedded; this is purely
  additive — keep the new path behind the env gate so the default
  `Go · integration` job is unaffected when LocalStack isn't present.
