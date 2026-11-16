- **test(coverage):** slice 313 — admin HTTP handler coverage lift
  (`internal/api/adminauditperiods` + `internal/api/adminsuperadmins` +
  `internal/api/admintenants` + `internal/api/adminvendors` +
  `internal/api/tenants`). Round-3 spillover from slice 312's coverage
  audit (`docs/coverage-audit-2026-05-round-3.md`); the audit identified
  five admin-scoped HTTP handler packages all below 70% merged coverage
  (each < 200 statements, parent slices 139 / 142 / 143 / 139 / 144)
  because none was enrolled in the CI integration job — every package
  shipped a comprehensive `*_integration_test.go` from its parent slice
  but the integration profile never reached merged coverage. Lifting
  strategy mirrors slices 290 / 297 / 310 / 315's "enrolment is the
  load-bearing move" playbook:
  1. Enrol `./internal/api/adminauditperiods/...`,
     `./internal/api/adminsuperadmins/...`, `./internal/api/admintenants/...`,
     `./internal/api/adminvendors/...`, and `./internal/api/tenants/...`
     in the `Go · integration (Postgres RLS)` job's `-coverpkg` list
     (`.github/workflows/ci.yml`).
  2. New `internal/api/adminauditperiods/helpers_test.go` covering the
     pure-Go pre-DB branches: `parseFormat` (empty / valid / invalid),
     `auditPeriodsExportHeader` (canonical column order),
     `periodsToRowIter` (open + frozen + early-yield-stop + empty),
     `callerAllowedExport` (admin / auditor / grc_engineer / unauthorized
     / empty-role-string), `WithLimiter` + `exportLimiter` (default
     singleton + override), `writeError`, `countingWriter.Write` (tally
     - error passthrough), `exportMetaAudit` JSON shape, and the
       no-credential 401 short-circuit branch.
  3. New `internal/api/adminvendors/helpers_test.go` mirroring the
     adminauditperiods shape: `parseFormat`, `vendorsExportHeader`,
     `vendorsToRowIter` (populated + nullable + email-mask + scope-cell
     join + early-stop + empty), `callerAllowedExport`, `WithLimiter`
     - `NewWithClock` + `exportLimiter`, `writeError`, `countingWriter`,
       plus the column-projection helpers `ptrToStr` / `boolStr` /
       `dateStr` (incl. UTC normalization) / `joinUUIDs`.
  4. New `internal/api/admintenants/helpers_test.go` covering the
     setter helpers (`WithLimit` / `WithClock`), `requireSuperAdmin`
     (granted / denied-no-claim / denied-no-context), `actorFromContext`
     (UUID subject / non-UUID / no claims), `actorTenantFromContext`
     (happy + missing), `mustMarshal` happy path, `stringPtr`,
     `actorAdvisoryKey` (determinism + slice-143 prefix + slice-142
     non-collision + distinct-actors-distinct-keys), `writeError` /
     `writeJSON`, and `tenantWire` omitempty semantics.
  5. Add five new floors to `cmd/scripts/coverage-thresholds.json`:
     `internal/api/adminauditperiods: 71`, `adminsuperadmins: 70`,
     `admintenants: 70`, `adminvendors: 75`, `tenants: 74`, each at
     `max(0, floor(measured - 2pp))` per slice 069's monotonic-ratchet
     methodology. `adminsuperadmins` and `tenants` reached the 70% bar
     on enrolment alone (consistent with slice 315 D2's "skip vanity
     unit tests when the existing integration suite already covers the
     load-bearing surface"); the three packages with new helper suites
     were below 70% post-enrolment and the unit tests for pure-Go pre-DB
     branches lift them to comfortable margins (adminauditperiods 73.9%,
     admintenants 72.9%, adminvendors 77.7%). Resolves the round-3 audit
     row for each of the five packages in `docs/issues/313-coverage-admin-http-handlers.md`.
