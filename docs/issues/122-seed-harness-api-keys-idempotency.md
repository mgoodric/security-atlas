# 122 — Make slice 082's `seedFromFixture()` harness idempotent on `api_keys` table

**Cluster:** Infra (CI hardening)
**Estimate:** 0.25d
**Type:** AFK
**Status:** `ready`

## Narrative

Surfaced during slice 119, captured as follow-up per continuous-batch policy.

Slice 082 (`web/e2e/seed.ts`) ships an idempotent `seedFromFixture(name)` harness that runs `fixtures/e2e/<name>.sql` files against Postgres + MinIO + NATS. The SQL fixtures use `ON CONFLICT DO NOTHING` for most tables, but the `api_keys` insertion path (which seeds the per-spec bearer token via HMAC-hashed value into the `api_keys.token_hash` column) does NOT have the conflict handling. Result: when multiple specs share the same test-bearer (or when the same spec re-runs in the same job), the second insert fails:

```
ERROR: duplicate key value violates unique constraint "api_keys_token_hash_unique"
DETAIL: Key (token_hash)=(\xe71af8beb1929b108a80e6d4a8abfdf4931bb4b8537c5be34d6ab38881bcbefc) already exists.
```

This crashed the Playwright e2e seed step in PR #259 (slice 119)'s CI run [25980065401](https://github.com/mgoodric/security-atlas/actions/runs/25980065401) — exposed only after slice 119's port-3000 fix let Playwright actually start running the specs. Before slice 119, the port-3000 race killed Playwright at startup, so this seed collision never surfaced.

The fix is a single statement change in `web/e2e/seed.ts` (or the `fixtures/e2e/00-seed.sql` file, depending on where the api_keys insert lives) — add `ON CONFLICT (token_hash) DO NOTHING` to the INSERT.

## Acceptance criteria

- [ ] AC-1: Audit `web/e2e/seed.ts` AND `fixtures/e2e/*.sql` for any INSERT into `api_keys` that lacks `ON CONFLICT DO NOTHING`. List the locations in the PR body.
- [ ] AC-2: Add `ON CONFLICT (token_hash) DO NOTHING` (or equivalent — if there's a different unique constraint being collided, use that one) to every such INSERT.
- [ ] AC-3: Validate by running `seedFromFixture()` twice in succession against a clean Postgres — second invocation MUST NOT throw a duplicate-key error. Document the local repro steps in the PR body.
- [ ] AC-4: CI run on the PR shows `Frontend · Playwright e2e` does NOT log any `duplicate key value violates unique constraint "api_keys_token_hash_unique"` error during the seed step.
- [ ] AC-5: Decisions log NOT required (mechanical fix; no judgment calls).

## Constitutional invariants honored

- **No vendor token prefixes** (slice 069's convention carries through — neutral `test-*` only)
- **CLAUDE.md "Surgical fixes only"** — one statement change, not a harness rewrite

## Canvas references

- `web/e2e/seed.ts` (slice 082's harness)
- `fixtures/e2e/*.sql` (the per-spec fixtures)
- Slice 082's decisions log (the original harness design)

## Dependencies

- None — slice 082 is merged; fix is self-contained.

## Anti-criteria (P0 — block merge)

- **P0-A1**: Does NOT change the `api_keys` table schema — fix is at the INSERT site, not the constraint.
- **P0-A2**: Does NOT touch any other table's idempotency. If other tables ALSO turn out to be non-idempotent in the same way, file separately (one fix per slice — the audit in AC-1 may surface candidates but they're out of scope for this slice).
- **P0-A3**: Does NOT use a vendor-prefixed token in any new fixture (carry-over convention).

## Notes for the implementing agent

- The token hash format (HMAC-SHA256 hex) is documented in slice 108's `internal/auth/credstore`. Don't try to compute a different hash to dodge the collision — fix the INSERT, not the test data.
- The seed harness uses `psql` subprocess invocation (slice 082 decision 1). The SQL statement landing the api_keys row is either in seed.ts as an inline `-c` query or in a `.sql` fixture file.
