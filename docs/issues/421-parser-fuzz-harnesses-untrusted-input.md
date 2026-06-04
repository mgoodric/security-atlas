# 421 — Parser fuzz harnesses for untrusted-input surfaces

**Cluster:** Quality
**Estimate:** 1-2d (M)
**Type:** AFK
**Status:** `merged` (`9d8e4179`, #985 — 5 fuzz targets + bounded CI; no crashers)
**Priority:** P1

## Narrative

**WHY.** The platform's thesis is ingesting untrusted files — OSCAL
bundles, SCF catalogs, questionnaire spreadsheets, manual CSV uploads,
schema-registry version strings — yet the repo has **zero** Go fuzz
targets today (`rg "func Fuzz" internal/ connectors/ pkg/` returns
empty). Every one of those parsers runs on operator- or
auditor-supplied input, and a malformed-input panic on any of them is a
denial-of-service on the ingest path with no test tier guarding it. The
project's four enforced surfaces (Go unit, Go integration, vitest,
Playwright — CLAUDE.md "Testing discipline") all assume well-formed
input; none probe the malformed-input frontier. Go's native `testing.F`
fuzzing closes that gap cheaply and is already available in the
toolchain.

**WHAT.** Native `testing.F` fuzz targets for the untrusted-input
parsers, each seeded from the package's existing golden fixtures, plus a
short bounded fuzz pass wired into CI so regressions surface on PR. The
target surfaces:

- **OSCAL bundle reader** — `internal/oscal/bundle.go` (the bundle
  read/parse path; the `_test.go` companion holds the golden fixtures).
- **SCF catalog importer** — `internal/api/scfimport/catalog.go`.
- **Schema-registry semver** — `internal/api/schemaregistry/semver.go`.
- **Manual CSV importer** — `connectors/manual/internal/manualcsv/csv.go`.
- **Questionnaire Excel parser** — `internal/questionnaire/excel.go`.

**SCOPE DISCIPLINE.** This slice adds fuzz _harnesses_ + a bounded CI
pass — it is NOT a "fix every bug fuzzing finds" slice. If a target
finds a crasher, the harness commits the minimized corpus entry and a
spillover slice is filed for the fix (continuous-batch Amendment 2). The
CI fuzz pass is bounded (`-fuzztime` short, e.g. 20-30s per target) so it
does not become a wall-clock tax; long-running fuzz campaigns are a
separate (deferred) concern. No new evidence_kind, no schema change, no
parser rewrite.

## Threat model

**S — Spoofing.** Not materially changed — fuzzing drives the parsers
directly with byte input; no new authenticated surface is added.

- Mitigation: fuzz targets are `testing.F` functions, compiled only
  under `go test`; they ship no runtime endpoint.

**T — Tampering (parser confusion).** A malformed OSCAL bundle / CSV /
Excel file that parses into a _different_ logical record than its bytes
imply (truncated multibyte, embedded null, smuggled field) could let
tampered input masquerade as a valid record.

- Mitigation: the fuzz corpus exercises boundary inputs (truncation,
  injected control bytes, oversized fields); a target asserts the parser
  either errors cleanly OR round-trips deterministically — never panics,
  never silently mis-parses into a divergent record.

**R — Repudiation.** No audit-log surface added; fuzzing is a test-tier
activity.

- Mitigation: n/a (no runtime mutation).

**I — Information disclosure.** A parser panic stack could surface
internal paths/state in logs at runtime.

- Mitigation: the AC asserts panics are converted to clean errors;
  corpus seeds are synthetic / golden-derived, never tenant data.

**D — Denial of service (HEADLINE).** Unbounded or malformed input
(deeply nested OSCAL JSON, multi-GB CSV line, pathological semver,
zip-bomb-shaped Excel) drives unbounded memory/CPU or a panic that
crashes the ingest worker.

- Mitigation: each fuzz target asserts no panic and bounded execution;
  where a parser lacks an input-size guard, the finding is a spillover
  fix slice, not silently absorbed. The CI fuzz pass is itself bounded
  (`-fuzztime`) so the harness cannot DoS CI.

**E — Elevation of privilege.** No new role check; parsers run under the
existing ingest identity.

- Mitigation: n/a (test-only addition).

**Verdict:** `has-mitigations`. DoS is the headline; the harnesses exist
precisely to convert latent parser panics into caught test failures.

## Acceptance criteria

- [ ] **AC-1.** A `func FuzzReadBundle` (or equivalently named) target
      lands for `internal/oscal/bundle.go`, seeded from the existing
      golden bundle fixtures.
- [ ] **AC-2.** A fuzz target lands for the SCF catalog importer
      (`internal/api/scfimport/catalog.go`), seeded from a valid catalog
      fixture.
- [ ] **AC-3.** A fuzz target lands for `internal/api/schemaregistry/semver.go`,
      seeded from valid + boundary version strings.
- [ ] **AC-4.** A fuzz target lands for the manual CSV importer
      (`connectors/manual/internal/manualcsv/csv.go`), seeded from a
      valid CSV fixture.
- [ ] **AC-5.** A fuzz target lands for the questionnaire Excel parser
      (`internal/questionnaire/excel.go`), seeded from a valid `.xlsx`
      fixture.
- [ ] **AC-6 (test).** Each fuzz target asserts the parser returns a
      clean error OR a deterministic value for every input — it never
      panics. Go's fuzz engine treats a panic as a failure; the AC
      verifies the target is structured so a panic is a fuzz failure,
      not a swallowed `recover()`.
- [ ] **AC-7 (test).** Each target runs cleanly under
      `go test -run=Fuzz<Name>` (the seed-corpus replay path) in the
      existing Go-unit job — i.e. seeds pass without `-fuzz`.
- [ ] **AC-8 (CI/test).** A bounded fuzz pass is wired into CI (a
      dedicated step or job) running `go test -fuzz=. -fuzztime=<short>`
      per target; a crasher fails the job and the engine writes the
      minimizing corpus entry to `testdata/fuzz/`.
- [ ] **AC-9.** Any crasher discovered during the slice is committed as a
      `testdata/fuzz/` corpus entry; the _fix_ is filed as a spillover
      slice (not bolted onto this slice).
- [ ] **AC-10.** Fuzz seed corpora contain only synthetic /
      golden-derived bytes — no tenant data, no vendor-prefixed secrets.
- [ ] **AC-11 (docs).** `web/testing.md` or `CONTRIBUTING.md` gains a
      short "Fuzzing" note: where targets live, how to run them locally
      (`go test -fuzz=FuzzX ./internal/oscal/`), and the spillover
      convention for crashers.

## Constitutional invariants honored

- **Ingestion and evaluation are separated (invariant #2).** Fuzzing
  hardens the ingestion-stage parsers; it never touches the evaluation
  stage or the evidence ledger.
- **Testing discipline (CLAUDE.md).** Adds a probing tier under the Go
  unit surface; the bounded CI pass keeps the wall-clock honest.
- **No proprietary collector agents.** Fuzzing the existing read-only
  parsers, not adding new ingest code paths.

## Canvas references

- `Plans/canvas/04-evidence-engine.md` — the ingestion stage + connector
  parsers under fuzz.
- `Plans/EVIDENCE_SDK.md` — the wire surface whose parsers ingest
  untrusted records.

## Dependencies

- None technical. All five target packages are `merged` on `main`. Slice
  is independently pickable.

## Anti-criteria (P0 — block merge)

- **P0-421-1.** Does NOT wrap any parser in a `recover()` to make a fuzz
  target "pass" — a panic must surface as a fuzz failure, not be
  swallowed.
- **P0-421-2.** Does NOT introduce an unbounded CI fuzz pass — the CI
  step MUST cap `-fuzztime` so the harness cannot itself DoS the runner.
- **P0-421-3.** Does NOT bolt parser _fixes_ onto this slice — crashers
  become committed corpus entries + spillover fix slices.
- **P0-421-4.** Does NOT seed corpora with tenant data or
  vendor-prefixed test tokens (neutral `test-*` / golden-derived only).
- **P0-421-5.** Does NOT lower or modify any existing coverage floor in
  `cmd/scripts/coverage-thresholds.json`.

## Skill mix (3-5)

- `tdd` (fuzz-target authoring)
- `engineering-advanced-skills:focused-fix` (per-parser seam)
- `Security` (STRIDE re-verification on crashers)
- `simplify` (pre-PR)

## Notes for the implementing agent

- The OSCAL reader lives at `internal/oscal/bundle.go` (not a
  `readbundle*` file — that was the brief's working name; the golden
  fixtures are in the `_test.go` companion). Seed `FuzzReadBundle` from
  those.
- Go fuzz seeds go in `testdata/fuzz/Fuzz<Name>/` next to the test; a
  crasher auto-writes there on failure — commit it.
- Keep `-fuzztime` short in CI (20-30s/target is enough for regression
  catching; deep campaigns are a maintainer/local activity). Consider a
  single `fuzz` job iterating the targets rather than per-package steps,
  to keep `ci.yml` churn minimal.
- The `internal/questionnaire` Excel parser uses `excelize`-shaped input
  (`excel.go`); a zip-archive-shaped fuzz input is the interesting
  boundary (malformed zip central directory). Seed from the existing
  `excel_test.go` golden `.xlsx`.
