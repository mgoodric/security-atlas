- **test(coverage):** slice 289 — `internal/artifact` coverage lift to
  90.2% merged (5.7% → 90.2%). New unit tests under
  `internal/artifact/store_test.go` (black-box) and
  `internal/artifact/internal_test.go` (white-box) exercise every
  pre-DB branch of `Store.Put` / `Get` / `Presign` / `LogAccess`
  (validation, hash-mismatch defence-in-depth, no-tenant-context guard,
  unreachable-pool Begin-error wrap), every `NewStore` config-validation
  rejection (missing Pool / S3 / Presigner / Bucket), the pure helpers
  `fromRow` + `pgUUID` directly (including the defence-in-depth branches
  for `ContentHash *string == nil` and `pgtype.Timestamptz.Valid ==
false` that the schema's NOT-NULL constraints make latent in
  production), and the `S3Presigner.PresignGetObject` adapter
  (presigning is a local-only signing op — verifies URL embeds bucket,
  key, X-Amz-Signature, and X-Amz-Expires matching the requested TTL).
  Floor in `cmd/scripts/coverage-thresholds.json` ratchets from `3` to
  `88` per the slice 069 methodology (`floor(measured - 2pp)`). Spillover
  from slice 279's coverage audit (`docs/coverage-audit-2026-05.md`).
