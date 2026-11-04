# OE-436 — Reconcile pkg/sdk-go retry/backoff contract

**Type:** JUDGMENT · **Date:** 2026-08-04

## D1 — Implement retry in pkg/sdk-go instead of weakening the contract

`Plans/EVIDENCE_SDK.md` promised automatic retry/backoff in the SDK surface and
said reference SDKs respect `Retry-After`. `pkg/sdk-go.Client.Push` previously
issued exactly one RPC, so the shipped Go reference SDK disagreed with the
contract.

The decision is to implement retry/backoff in `pkg/sdk-go` rather than amend the
contract into a caller responsibility. The Go SDK is the first-party SDK used by
connectors and middleware, so making every caller hand-roll the same transient
transport loop would spread correctness requirements across connector code.
Centralizing the behavior lets the SDK enforce the important invariant: a retry
must re-send the same record and idempotency key bytes so ledger dedup absorbs a
re-send instead of producing an idempotency mismatch.

The implementation is intentionally narrow:

- Retry only gRPC transport-class failures: `Unavailable` and
  `DeadlineExceeded`.
- Treat application statuses as terminal. `AlreadyExists` is never retried.
- Use a documented, configurable default of five total attempts: the initial
  RPC plus 1s, 2s, 4s, and 8s exponential-backoff retries with 20% jitter.
- Honor `Retry-After` metadata when it is present on a retryable status, capped
  by the configured maximum delay.
- Keep `Push(ctx, record)` source-compatible; callers can override behavior via
  `WithRetryConfig`, including `MaxAttempts: 1` to disable retry.

## D2 — Retry is not the credential-restart remedy

This closes only the SDK/spec divergence reported by slice 356b G-2. The chaos
run's D1 showed that applying the design's own 1s/2s/4s/8s retry schedule
around the existing single-shot push recovered zero records under the atlas
container restart experiment: the retries reached a restarted process that
returned `Unauthenticated` because the credential store had been wiped.

That credential invalidation is a separate higher-severity gap. This slice does
not change credential storage, token refresh, ledger idempotency, or dedup
semantics.
