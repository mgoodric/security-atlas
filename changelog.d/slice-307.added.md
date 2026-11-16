- **test(coverage):** slice 307 — `connectors/manual/cmd/atlas-manual`
  coverage lift from 43.6% to 73.8% merged. A new
  `connectors/manual/cmd/atlas-manual/cmd_coverage_test.go` adds 39 unit
  tests covering the cobra glue (`newRootCmd` smoke + subcommand wiring
  for register + scopes + local + s3 + sftp + persistent-flag presence;
  `newRegisterCmd` PreRunE missing-env error path + RunE
  unreachable-endpoint RPC-error path; `newLocalCmd` PreRunE missing
  `--file` / missing `--scope` / resolveCommon fall-through;
  `newS3Cmd` PreRunE missing `--bucket` / missing `--prefix` / missing
  `--scope` / resolveCommon fall-through; `newSFTPCmd` PreRunE missing
  `--host` / `--user` / `--path` / `--scope` / resolveCommon
  fall-through; `newScopesCmd` Run path renders all three documented
  modes through tabwriter), env-var + global-flag resolution
  (`resolveCommon` 77.8% → 100% via six branches: endpoint/token ×
  flag/env/missing), both dial-transport branches
  (`dialConnectorRegistry` 0% → 87.5% — TLS + insecure both lazy-dial
  against 127.0.0.1:1 with no network IO), the `authedContext`
  metadata wiring (0% → 100% with Authorization header assertion +
  cancel-func verification), `sdkOpts` insecure vs secure paths
  (66.7% → 100%), `connectorVersion` smoke, `actorID` shape pinning
  across all three services (local / s3 / sftp), `doLocal` end-to-end
  drives (missing-file open-error path, malformed-scope parse-error
  path, push-error-against-unreachable-platform path — pushing 68.6%
  → 74.3%), `doS3` end-to-end drives via a fake S3 HTTP server
  returning empty + populated `ListBucketResult` XML (28.1% → 87.5% —
  exercises `config.LoadDefaultConfig` + `s3.NewFromConfig` +
  `manuals3.List` + `parseScope` + `sdk.NewClient` + the empty-loop
  success-print path + the populated-loop push-error wrap), `doSFTP`
  early-error paths (missing key-file + missing known-hosts, with the
  post-dial sftp/ssh path left for integration coverage — the seam
  requires a real SSH server which is an integration-test surface),
  `buildLocalRecord` happy + header-less row paths (66.7% maintained,
  the payload-too-large skip branch is fired by an integration test
  not a unit test — captured in the slice notes), `buildS3Record` +
  `buildSFTPRecord` schema-required field assertions (0% → 75% each
  with `evidence_kind` / `schema_version` / `control_id` /
  `idempotency_key` / payload-map keys pinned), and `guessContentType`
  full extension table (0% → 100% — csv / json / pdf / txt / log /
  xml / html / htm / fallback). Honors canvas §4.5 (manual evidence
  is first-class — the per-mode cobra glue exercised on the same
  footing as automated connectors). No vendor-prefixed tokens appear
  in fixtures — neutral `test-*` strings only, per `CLAUDE.md`'s hard
  rule. Floor in `cmd/scripts/coverage-thresholds.json` ratchets from
  `41` to `71` per slice 069's `floor(measured - 2pp)` methodology.
  Closes [#307](docs/issues/307-coverage-connectors-manual-cmd.md).
