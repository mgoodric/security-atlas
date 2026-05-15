# 068 — Schema-registry evidence_kind identifier fix — decisions log

Slice 068 is `Type: AFK` — its acceptance criteria are mechanically
verifiable. It surfaced one significant build-time judgment call: the
issue doc, as orchestrator-authored in a prior reconcile, described the
fix in the **wrong direction**. The `grill-with-docs` gate (run before
implementation, against `Plans/EVIDENCE_SDK.md` §4.5 and
`Plans/canvas/04-evidence-engine.md`) caught it. This log records the
correction in the JUDGMENT-slice format (Decisions made · Revisit once in
use · Confidence per decision). It does NOT block merge.

**Scope note (PR #125).** The initial implementation (decisions 1–3) was
the evidence_kind identifier alignment. It was correct but did not green
slice 065's `test-self-host-bundle` e2e job — a CI-driven root-cause pass
found two further independent defects (a boot-time schema-cache race and a
distroless-image `/health` probe bug) plus a harness diagnostics gap.
Those are decision 4 below; the slice scope grew from "yaml identifier
alignment" to "fresh-deploy control-bundle upload works end-to-end in both
self-host deploy shapes".

## Decisions made

### 1. The fix direction in the original issue doc was INVERTED — the canvas wins

**The drift.** The original `docs/issues/068-*.md` stated:

> - "the SOC2 control bundles ... reference the **bare** kind ... the
>   evidence-push path ... uses bare kind"
> - AC-1: "`DefaultSeed()` ... returns bare `Kind` strings (no `.v1`
>   suffix)"
> - Anti-criterion: "Does NOT change the on-the-wire evidence_kind
>   identifiers that connectors/pushers already emit — those use the bare
>   convention; the fix aligns `DefaultSeed()` TO that convention."

This is factually wrong. Ground-truth audit of the codebase:

| Source                               | Form                        | Evidence                                                                                                                                                                      |
| ------------------------------------ | --------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `Plans/EVIDENCE_SDK.md` §4.5         | **`.v1`-suffixed**          | "A stable identifier (`sast.scan_result.v1`, `access_review.completion.v1`, `manual.attestation.v1`)" + push-record example line 119 `"evidence_kind": "sast.scan_result.v1"` |
| `Plans/EVIDENCE_SDK.md` §6 (CLI)     | **`.v1`-suffixed**          | `--kind sast.scan_result.v1`                                                                                                                                                  |
| `docs/issues/014` AC-3               | **`.v1`-suffixed**          | "ships ~10 v1 platform schemas: `sast.scan_result.v1`, ..."                                                                                                                   |
| All 9 first-party connectors         | **`.v1`-suffixed**          | `EvidenceKind: "osquery.host_posture.v1"`, AWS `SupportedKind = "aws.s3.bucket_encryption_state.v1"`, etc.                                                                    |
| `DefaultSeed()`                      | **`.v1`-suffixed**          | `{Kind: "osquery.host_posture.v1", Version: "1.0.0"}`                                                                                                                         |
| All 16 bundled JSON schema files     | **`.v1`-suffixed**          | `"x-evidence-kind": "osquery.host_posture.v1"`                                                                                                                                |
| `cmd/atlas-cli/cmd_evidence.go` help | **`.v1`-suffixed**          | `"evidence_kind (e.g., sast.scan_result.v1)"`                                                                                                                                 |
| **`controls/soc2/*/control.yaml`**   | **bare** ← the actual drift | `evidence_kind: osquery.host_posture`                                                                                                                                         |

The push path uses a `.v1`-suffixed `EvidenceKind` plus a separate
`SchemaVersion` (`"1.0.0"`) — exactly the canvas's "stable identifier +
separate semver" shape. Had the slice followed AC-1 as written (strip
`.v1` from `DefaultSeed()`), it would have broken registry lookup for
every connector push and contradicted `EVIDENCE_SDK.md` — the very
canvas the issue cites.

**Chosen.** Per the per-slice-template rule "if an issue's AC contradicts
the canvas, the canvas wins — correct the issue's AC," the canonical
convention is **`.v<major>`-suffixed kind identifier + separate
`schema_version` semver**, per `EVIDENCE_SDK.md` §4.5. The fix:

1. Align the 13 distinct SOC 2 control-bundle `evidence_kind` references
   (across 28 bundles) TO the canonical `.v1`-suffixed identifier.
2. Leave `DefaultSeed()`, the connectors, the SDK, the push path, and the
   schema files' `x-evidence-kind` **untouched** — already correct.
3. Rewrite `docs/issues/068-*.md` ACs + narrative to the correct
   direction (done in this PR).

**Confidence: high.** `EVIDENCE_SDK.md` §4.5 is unambiguous and is the
canvas reference the issue itself cites; every on-the-wire consumer
independently corroborates it. The grill maintainer reviewed and
explicitly accepted this resolution before implementation began.

### 2. `internal/control/validate.go` needs NO code change

**The question.** AC-2 asks whether `registryKnowsKind` needs a change to
resolve `.v1`-suffixed kinds.

**Chosen.** No change. `registryKnowsKind` probes
`reg.IsRegistered(kind, "1.0.0")` with the bundle's `evidence_kind`
verbatim. Once the bundles carry `osquery.host_posture.v1`, the probe is
`IsRegistered("osquery.host_posture.v1", "1.0.0")` — which is exactly the
`(Kind, Version)` pair the registry holds. Verified by
`TestEvidenceKindDrift_SOC2BundlesPassRegistryValidation`, which drives
the real `Bundle.ValidateEvidenceKinds` -> `registryKnowsKind` path
against a `DefaultSeed()`-seeded registry for all 50 SOC 2 bundles and
passes. Adding suffix-resolution magic to `validate.go` was explicitly
rejected as the inferior option (b-vs-a in the grill) — it would couple
the validator to an identifier-rewriting convention rather than letting
both sides simply agree on one canonical string.

**Confidence: high** — proven by an integration-style test over the real
validation code and the real bundle files.

### 3. Schema _directory_ names left bare — cosmetic, not load-bearing

**The question.** The `internal/api/schemaregistry/schemas/` directories
are bare (`osquery.host_posture/`) while their `x-evidence-kind` values
are `.v1`-suffixed. Should the directories be renamed to match?

**Chosen.** Left as-is. `LoadPlatformSchemas` already strips the
`.v<major>` suffix from `x-evidence-kind` to derive the _expected_
directory name, and that path-consistency check passes today. The
registered identifier comes from `x-evidence-kind` (`.v1`-suffixed), not
the directory name — so the directory name is a discovery breadcrumb,
not a contract surface. Renaming 16 directories is pure churn with a
non-zero chance of breaking the `//go:embed all:schemas` glob or the
loader's path check, for zero functional gain. The drift-guard test
asserts the identifiers (`x-evidence-kind` ⇔ `DefaultSeed()` ⇔ SOC 2
bundles) are consistent; it deliberately does not assert directory names,
because directory names are not part of the convention.

**Confidence: medium-high** — the reasoning is sound, but a future
contributor browsing `schemas/` may briefly be confused by the bare
directory vs `.v1` `x-evidence-kind`. Mitigated by the existing
`embedded_fs.go` comment and the new `DefaultSeed()` convention comment.

### 4. The identifier alignment was necessary but NOT sufficient — two further root causes surfaced in CI

The identifier fix (decision 1) was correct and stays. But it did **not**
green slice 065's `test-self-host-bundle` e2e job — that job stayed red in
**both** matrix modes. A CI-driven root-cause pass (PR #125) found two
further, independent defects, neither of which is about the `.v1` vs bare
identifier:

**4a. Boot-time schema-cache hydration raced `atlas_app`'s password.**
The `external` deploy shape kept 400'ing `evidence_kind
"osquery.host_posture.v1" is not registered` even with the bundles
aligned. The atlas server stderr (now visible — see 4c) showed the real
chain:

```
atlas: schema import inserted=16 total=16
atlas: schema cache reload: list global schemas: failed to connect to
  `user=atlas_app ...`: failed SASL auth: FATAL: password authentication
  failed for user "atlas_app" (SQLSTATE 28P01)
```

`cmd/atlas` imports the bundled schemas into Postgres via the BYPASSRLS
**migrate** pool — that succeeds (`inserted=16`). It then hydrates its
**in-memory** registry cache via the RLS-bound **app** pool
(`schemaSvc.LoadFromDB`). But the self-host bundle starts `atlas` in
parallel with `atlas-bootstrap` (`depends_on: service_started`, a slice-065
change), and `atlas_app`'s password is set by `bootstrap.sh` phase 2.5
(`ALTER ROLE atlas_app PASSWORD ...`) — which races atlas's boot. On a
scram-sha-256 cluster (external mode) the single `LoadFromDB` attempt lost
that race and failed `28P01`; the in-memory cache stayed empty;
`controlsRegistry.IsRegistered` returned false for every kind; phase 6's
control-bundle upload 400'd. The `bundled` mode masked this because
`POSTGRES_HOST_AUTH_METHOD=trust` lets `atlas_app` connect password-less
regardless of whether the password is set yet.

**Chosen.** Make the boot-time cache load resilient: `cmd/atlas` now
retries `LoadFromDB` with a fixed 2s backoff for up to ~90s
(`retrySchemaCacheLoad`) until the app role is authenticable. The schema
rows were already durably written via the migrate pool, so this only
waits for the app pool to become connectable — bootstrap sets the password
within seconds. The HTTP listener still starts only **after** this
completes, so by the time control-bundle uploads or `/health` probes
arrive the in-memory registry is hydrated. The retry respects `ctx`
cancellation (SIGTERM during boot is honoured) and stays non-fatal on
exhaustion (matches the prior log-and-continue behaviour). No deadlock:
bootstrap phase 2.5 (sets password) runs before phase 5 (waits for atlas
`/health`), and atlas's retry budget (90s) sits inside phase 5's wait
budget (180s).

**Confidence: high** — the failure mode is unambiguous in the atlas
stderr, the fix targets exactly the raced call, and the ordering analysis
shows no new deadlock.

**4b. The e2e harness's `/health` probe could never pass against the
distroless atlas image.** The `bundled` mode failed with `atlas /health
never returned 200` even though atlas booted cleanly and `atlas-bootstrap`
exited 0 (which means bootstrap's _own_ phase-5 `/health` poll — run from
the alpine-based bootstrap container, over the Docker network — _had_
succeeded). The harness probed liveness with `docker exec atlas wget ...`,
but the atlas image is `gcr.io/distroless/static-debian12` — no shell, no
`wget`, no `curl`. `docker exec atlas wget` therefore _always_ fails
("executable not found"), independent of server health.

**Chosen.** The harness now resolves atlas's host-published `:8080` port
(`docker compose port atlas 8080`) and `curl`s `/health` from the CI
runner. This is both correct (the runner has `curl`) and a more faithful
smoke test than an in-container loopback probe — it exercises the same
path a real operator's browser or load balancer takes.

**Confidence: high** — distroless images demonstrably have no `wget`; the
host-port path is what the bundle already documents for operator access.

**4c. The harness destroyed the evidence on every failure.** The blocker
that made 4a/4b hard to diagnose: the harness's `cleanup()` EXIT trap runs
`docker compose down -v` on every `fail()`, so CI's "Dump compose logs on
failure" step always ran against an already-destroyed stack and printed
nothing. **Chosen.** `fail()` now dumps full compose logs for all services
(`--tail=300`) to stdout _before_ `exit 1` triggers the cleanup trap.
`cleanup()` is unchanged (it still tears the stack down afterward). This is
what made the atlas stderr in 4a visible at all.

**Confidence: high** — purely additive diagnostics; no behaviour change to
the assertions.

**4d. Harness assertion 5 was checking a table the bootstrap flow never
writes.** With 4a/4b fixed, the e2e job advanced past `/health` and
control-bundle upload (`controls` reached 50 rows in both modes — the
identifier + cache-race fixes demonstrably worked) and then failed a NEW
assertion: `api_keys row count = 0, want >= 1`. Assertion 5's stated
intent was "prove the audit-writer fix unblocked phase 6 (slice 065 bug
#1)" — but it asserted on the wrong table. **Nothing in the bootstrap flow
writes `api_keys`:** `seed.sql` does not, the SCF import does not, and the
control-bundle upload does not. `api_keys` is the slice-034 DB-backed key
store, written only by the `/v1/admin/credentials` HTTP route. The
bootstrap uploader authenticates with the **in-memory** fixed-token
credential (`IssueBootstrapFixedAdminCredential` →
`credStore.IssueFixedAdmin`), never a DB row. The assertion could never
have passed — slice 065 merged with this e2e job red, so it had never
actually run green. The `cmd/atlas/main.go` comment that claimed "phase 6
completing populates api_keys as designed" was the same misconception in
source form.

**Chosen.** Re-point assertion 5 at `decision_audit_log`. That IS the
table slice 065 bug #1 was about: the bug was an RLS-blind write to
`decision_audit_log` in the OPA authz audit writer, which 500'd every
authenticated request and blocked phase 6 entirely. Every authenticated
request — including phase 6's 50 control-bundle uploads, which mount under
`authzmw.Middleware` — writes one decision row there. A populated
`decision_audit_log` therefore proves exactly what assertion 5's comment
always intended: phase 6's authenticated path ran AND bug #1's fix held.
The misleading `cmd/atlas/main.go` comment was corrected in the same
change.

**Confidence: high** — `decision_audit_log` is demonstrably the
slice-065-bug-#1 table (`internal/authz/audit.go` writes it), the control
upload route demonstrably mounts under the authz middleware, and the
corrected assertion tests a real, causally-linked post-condition rather
than an unrelated table.

**4e. Control-bundle RE-upload was never idempotent — wrong supersession
order + a non-deferrable self-FK, masked by a test that CI never ran.**
With 4a–4d fixed, the e2e job advanced to its LAST assertion (assertion 6,
AC-7: re-running `atlas-bootstrap` against the populated DB must exit 0)
and failed in both modes: phase 6's re-upload of the 50 SOC 2 bundles
500'd with

```
persist: insert control version: ERROR: duplicate key value violates
unique constraint "controls_one_active_version_per_bundle" (SQLSTATE 23505)
```

Slice 009 shipped, in the same migration, (a) a partial unique index —
at most one `controls` row per `(tenant_id, bundle_id)` with
`superseded_by IS NULL` — and (b) the self-FK `controls_superseded_by_fk`
created NON-deferrable. The slice-009 SQL itself documents the only order
that satisfies the index: flip the predecessor to superseded BEFORE
inserting the new active row. But that order is impossible with a
non-deferrable self-FK (the UPDATE points the predecessor at a row that
does not exist yet), so `internal/control/store.go::Upload` did
insert-then-update instead — and insert-then-update is the order the
unique index rejects, because for one statement-instant both the old and
the new row have `superseded_by IS NULL`. Net effect: the FIRST upload of
any bundle worked, every RE-upload 500'd. This shipped on `main` for ~59
slices.

It went undetected because `internal/control/integration_test.go` already
HAS the test that catches it (`TestUpload_ReuploadSupersedes`, slice-009
AC-6) — but the CI integration job's hand-maintained `-coverpkg` package
list never included `./internal/control/...`, so that test had never run
in CI. The test had also bit-rotted against ~59 slices of schema drift (it
referenced a `frameworks.source` column and a `framework_versions.release_version`
column that no longer exist, and its cleanup helper NULLed every version
of a bundle at once — itself a 23505).

**Chosen.** Four coordinated changes:

1. New migration `20260511000033_controls_superseded_fk_deferrable.sql`
   re-creates `controls_superseded_by_fk` as `DEFERRABLE INITIALLY
DEFERRED` (validated at COMMIT). This is the same pattern slice 002
   already uses for `frameworks_latest_version_fk` — a sibling
   "row points at a row created in the same transaction" relationship.
   (Originally drafted at slot `_032`; renumbered to `_033` because
   slice 032's in-flight `20260511000032_board_packs.sql` (PR #126) had
   reserved `_032`. `sqlc.yaml` updated to match.)
2. `store.go::Upload` reordered to mark-predecessor-superseded THEN
   insert-the-new-row — the order slice 009's own SQL prescribes, now
   possible because the FK check is deferred.
3. **`store.go::Upload` made a true no-op for byte-identical re-uploads.**
   The deferrable-FK + reorder (1 + 2) makes a CHANGED-content re-upload
   correctly version-bump, but a re-upload of UNCHANGED content would
   still create an identical "version 2" — meaningless version churn,
   and it still fails the slice-065 idempotency assertion (AC-7), which
   counts `controls` rows and expects them stable across a bootstrap
   re-run (50 -> 100 on re-upload). Slice 009 AC-6 ("re-uploading the
   same bundle id creates a new control row and supersedes the prior")
   is about CHANGED content; identical content has nothing to supersede.
   `Upload` now compares the active version's `bundle_manifest_hash`
   (sha256 of the manifest YAML) to the incoming bundle's hash and, on a
   match, returns the existing row unchanged (`IsNewBundle=false`,
   `SupersededID` zero, no INSERT, no UPDATE). This is also what
   `bootstrap.sh`'s header already claims control upload does ("upsert")
   — the claim was simply not true before this change. The HTTP handler
   already maps `!IsNewBundle` to 200; it now also omits `superseded_id`
   from the response when nothing was superseded.
4. Wired `./internal/control/...` into the CI integration job's package
   list, and repaired the bit-rotted `seedSCFAnchor` + `freshTenant`
   helpers, so `TestUpload_ReuploadSupersedes` actually runs and guards
   this going forward.

**Confidence: high** — verified locally end-to-end: all 33 migrations
(incl. `_033`) apply clean, the `_033` down/up round-trip flips the
constraint's deferrability cleanly, and the full
`internal/control` integration suite — including
`TestUpload_ReuploadSupersedes` — passes against a CI-parity Postgres.
The byte-identical-no-op path is additionally proven by the new
`TestUpload_ReuploadIdenticalIsNoop` integration test and confirmed
green by the self-host e2e job's AC-7 idempotency re-run.

## Revisit once in use

- **Decision 3** — if the bare-directory / `.v1`-`x-evidence-kind`
  mismatch trips up a contributor, a follow-up slice can rename the
  directories to the full `.v1` identifier in one mechanical pass
  (updating the loader's path check to stop stripping the suffix). Low
  priority; cosmetic.
- **Decision 2** — if a future control-bundle format ever lets authors
  pin a non-`1.0.0` baseline semver, `registryKnowsKind`'s "try 1.0.0
  first" heuristic will need revisiting. Out of scope here; the bundle
  format does not expose a semver field today.
