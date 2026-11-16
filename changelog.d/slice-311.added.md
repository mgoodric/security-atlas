- **test(coverage):** slice 311 — `internal/auth/bearer` coverage lift
  from 70.0% to 95.0%. Round-2 coverage slice surfaced during the
  post-batch-122 audit as the smallest gap-to-70% in the round (floor
  was `68` in `cmd/scripts/coverage-thresholds.json`; measured was
  already 70.0% with no margin against the slice 069 ratchet). First
  action per the slice doc was an import grep
  (`grep -rn '"github.com/mgoodric/security-atlas/internal/auth/bearer"' .`)
  to determine whether the package was dead code retired by slice 197's
  JWT-bearer cutover — it is not: live importers are `cmd/atlas/main.go`,
  `cmd/atlas-cli/cmd_oauth_migrate.go`, and
  `internal/auth/apikeystore/apikeystore.go`. The package wraps HMAC-SHA256
  bearer-token hashing (per [`docs/adr/0002-bearer-token-storage.md`](docs/adr/0002-bearer-token-storage.md))
  and is still on the live API-key authentication path that the apikeystore
  package builds on. Path-B (lift) chosen per the slice's EITHER/OR
  decision tree. Five new unit tests in
  `internal/auth/bearer/bearer_test.go` exercise the three uncovered
  branches: `LoadHashKey` (0% → 100%) via `t.Setenv("BEARER_HASH_KEY", …)`
  on the unset-and-empty branch (returns `ErrHashKeyMissing`), the
  present-but-shorter-than-32-bytes branch (also returns
  `ErrHashKeyMissing`), and the happy path with a 32-byte key (returns
  the bytes verbatim with no error); `Last4` (66.7% → 100%) via a
  table-driven test of the short-token branch — empty string, one char,
  three chars, and the exact-four-char boundary that hits the
  `len(token) >= 4` slice path. The remaining 5pp gap is `Generate`'s
  `rand.Read` error wrap which requires a `crypto/rand` seam injection
  not worth the test-only refactor (current 85.7% coverage on
  `Generate` covers happy + bad-prefix branches). Floor in
  `cmd/scripts/coverage-thresholds.json` ratchets from `68` to `93`
  per slice 069's `floor(measured - 2pp)` methodology. No
  vendor-prefixed tokens in fixtures, per `CLAUDE.md`'s hard rule —
  the existing `atlas_test_abcdwxyz` and new `too-short-not-32-bytes` /
  `strings.Repeat("k", 32)` strings are all neutral. Closes
  [#311](docs/issues/311-coverage-auth-bearer.md).
