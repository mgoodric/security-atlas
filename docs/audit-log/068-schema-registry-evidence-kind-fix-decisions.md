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
