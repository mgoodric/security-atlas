# 068 — Schema-registry evidence_kind identifier fix

**Cluster:** evidence-pipeline
**Estimate:** 1-1.5d
**Type:** AFK

> **Note (slice 068, grill correction):** the original draft of this issue
> described an _inverted_ fix direction — "strip `.v1` from `DefaultSeed()`,
> connectors already emit bare names." Grilling against the cited canvas
> (`Plans/EVIDENCE_SDK.md` §4.5) proved the opposite: the canonical
> identifier is `.v1`-suffixed, and every on-the-wire consumer (9
> connectors, 16 JSON schema files, the SDK, the push CLI, `DefaultSeed()`
> itself, slice 014's AC-3) already agrees. The only drift was the SOC 2
> control bundles referencing bare names. Per the per-slice-template
> "issue AC contradicts the canvas -> canvas wins" rule, this issue doc was
> rewritten to the correct fix direction. See
> `docs/audit-log/068-schema-registry-evidence-kind-fix-decisions.md`.

## Narrative

Fix a pre-existing evidence_kind identifier inconsistency that breaks fresh-deploy control-bundle upload. Surfaced by slice 065's AC-12 self-host end-to-end CI job: once the self-host bundle boots cleanly (all 33 migrations + seed + atlas startup), bootstrap phase 6 — "upload control bundles from `/repo/controls/soc2`" — fails with:

```
error: upload failed: HTTP 400: control bundle: evidence_kind "osquery.host_posture" is not registered in the schema registry
```

### The bug

The canonical evidence_kind identifier convention — per `Plans/EVIDENCE_SDK.md` §4.5 — is a **`.v<major>`-suffixed kind identifier** (`osquery.host_posture.v1`) paired with a **separate `schema_version` semver** (`1.0.0`). The `.v<major>` suffix is part of the stable identifier; the semver tracks additive evolution within that major.

Every on-the-wire / canonical consumer already honors this convention:

- **`DefaultSeed()`** in `internal/api/schemaregistry/registry.go` registers `{Kind: "osquery.host_posture.v1", Version: "1.0.0"}` — correct.
- **The bundled JSON Schemas** (`internal/api/schemaregistry/schemas/osquery.host_posture/1.0.0.json`) carry `x-evidence-kind: "osquery.host_posture.v1"` — correct. (The _directory_ name is bare, but `LoadPlatformSchemas` strips the `.v<major>` suffix to derive the expected directory, so the loader registers the `.v1` identifier from `x-evidence-kind` regardless.)
- **All 9 first-party connectors** emit `.v1`-suffixed kinds (`EvidenceKind: "osquery.host_posture.v1"`, AWS `SupportedKind = "aws.s3.bucket_encryption_state.v1"`, etc.) — correct.
- **The Evidence SDK push path + CLI** use `.v1`-suffixed kinds (`--kind sast.scan_result.v1`) — correct.
- **Slice 014's AC-3** explicitly lists the `.v1`-suffixed kind set — correct.

The single point of drift: **the SOC 2 control bundles** (`controls/soc2/*/control.yaml`, slice 010) referenced the **bare** kind name — `evidence_kind: osquery.host_posture`. On control-bundle upload, `internal/control/validate.go::registryKnowsKind` probes the registry with the bundle's bare name; the registry holds only the `.v1` form; the lookup misses; the upload 400s.

This affects **every evidence_kind the SOC 2 bundles reference** (13 distinct kinds across 28 bundles); `osquery.host_posture` is just the first bundle the bootstrap loop hits.

The bug was latent because nothing exercised "fresh bootstrap -> upload control bundles" end-to-end until slice 065's AC-12 job. **It means fresh-deploy control-bundle upload is broken on `main` today** — slice 037's "4-hour-to-first-evidence" demo path does not actually work from a clean checkout.

The identifier alignment is a small, bounded change: the SOC 2 control bundles are aligned to reference the canonical `.v1`-suffixed identifier the registry, connectors, schema files, and SDK already use. No registry / connector / SDK change is needed for the identifier itself — they were already correct.

> **Scope grew (PR #125).** The identifier alignment alone did **not** green slice 065's `test-self-host-bundle` e2e job — that job stayed red in both matrix modes. A CI-driven root-cause pass found two further independent defects that also block fresh-deploy control-bundle upload:
>
> 1. **Boot-time schema-cache race.** `cmd/atlas` imports the bundled schemas via the BYPASSRLS migrate pool (succeeds) but hydrates its in-memory cache via the RLS-bound `atlas_app` pool — and the self-host bundle starts `atlas` in parallel with `atlas-bootstrap` (`depends_on: service_started`), so `LoadFromDB` races `bootstrap.sh` phase 2.5's `ALTER ROLE atlas_app PASSWORD`. On a scram-sha-256 cluster (external mode) the single attempt loses the race (`SQLSTATE 28P01`), the in-memory registry stays empty, and every control-bundle upload 400's. Fixed by retrying the boot-time cache load with backoff (`cmd/atlas/main.go::retrySchemaCacheLoad`).
> 2. **Distroless `/health` probe bug.** The slice-065 harness probed atlas liveness with `docker exec atlas wget` — but the atlas image is distroless (no shell, no wget), so the probe always failed regardless of server health (the bundled-mode false failure). Fixed by curling atlas's host-published port from the CI runner. The harness also now dumps compose logs on failure before its cleanup trap runs, so future failures stay diagnosable.
>
> See `docs/audit-log/068-schema-registry-evidence-kind-fix-decisions.md` decision 4. The slice delivers value because it unbreaks the self-host bundle's last broken phase and greens slice 065's AC-12 end-to-end job.

## Acceptance criteria

- [ ] AC-1: Every `controls/soc2/*/control.yaml` `evidence_kind` reference uses the canonical `.v<major>`-suffixed identifier (`osquery.host_posture.v1`, `sast.scan_result.v1`, etc.) — matching `DefaultSeed()`, the bundled schema files' `x-evidence-kind`, and the convention in `Plans/EVIDENCE_SDK.md` §4.5.
- [x] AC-2: Confirm `internal/control/validate.go::registryKnowsKind` resolves the `.v1`-suffixed kinds once the bundles are corrected (it probes `IsRegistered(kind, "1.0.0")`; `osquery.host_posture.v1` + `1.0.0` is exactly what the registry holds). If a `validate.go` change is needed, make it; if not, verify and record that no change was required. **Done:** no `validate.go` change required (decisions log section 2). The e2e job did reveal, though, that this probe is only as good as the in-memory registry it reads, and `cmd/atlas` was not reliably populating that registry at boot (see AC-3).
- [x] AC-3: A fresh-deploy bootstrap's phase-6 control-bundle upload succeeds — every `evidence_kind` referenced by every `controls/soc2/*/control.yaml` resolves in the registry. Verify by driving the real `Bundle.ValidateEvidenceKinds` path against an in-memory registry seeded from `DefaultSeed()` for every SOC 2 bundle. **Done:** the unit-level path is verified by the drift-guard test; the e2e path additionally required the `cmd/atlas` boot-time schema-cache retry fix (decisions log section 4a) — the running server's in-memory registry was empty because `LoadFromDB` raced the `atlas_app` role password.
- [ ] AC-4: Slice 065's `test-self-host-bundle` CI matrix job passes in **both** modes (bundled + external) — the end-to-end proof. (Slice 065's AC-12 has been red pending this slice; 068 is what greens it.) **Note:** required the `cmd/atlas` retry fix (4a) plus the harness `/health` probe fix (4b) on top of the identifier alignment.
- [ ] AC-5: A drift-guard test asserting mutual consistency: (a) every evidence_kind referenced by `controls/soc2/*` resolves in `DefaultSeed()`; (b) the `schemas/*/` `x-evidence-kind` set equals the `DefaultSeed()` kind set; (c) every kind in both sets carries a `.v<major>` suffix. This prevents silent recurrence — the inconsistency was latent for ~14 slices precisely because nothing asserted it.
- [ ] AC-6: Audit the other evidence_kind consumers — the Evidence SDK push path, the connectors (`connectors/*`) — confirming they already use the canonical `.v1`-suffixed convention (they do). Document the canonical convention (`.v<major>`-suffixed kind identifier + separate semver) in a comment at the `DefaultSeed()` definition site so the next contributor does not reintroduce the bare-name drift.
- [ ] AC-7: `CHANGELOG.md` entry under `[Unreleased]/Fixed`.

## Constitutional invariants honored

- **Invariant 3 (one canonical inbound Evidence API):** the evidence_kind identifier is part of the `IngestEvidence` contract — this slice makes the SOC 2 control bundles agree with the identifier convention the registry, push path, and connectors already honor.
- **Working norms — cite sources:** the canonical convention gets a comment at the `DefaultSeed()` definition site (citing `Plans/EVIDENCE_SDK.md` §4.5) so the next contributor does not reintroduce the `.v1`/bare drift.

## Canvas references

- `Plans/canvas/04-evidence-engine.md` (Evidence SDK + evidence_kind schemas)
- `Plans/EVIDENCE_SDK.md` §4.5 (the `IngestEvidence` contract — evidence_kind identifier shape; the source of truth for this slice)
- `docs/issues/014-schema-registry-service.md` (the schema registry; its AC-3 lists the canonical `.v1`-suffixed kind set)
- `docs/issues/010-soc2-control-kit.md` (the control bundles whose evidence_kind references had drifted to bare names)

## Dependencies

- **014** (schema registry service — `DefaultSeed()` + the file loader live here)
- **010** (SCF-anchored SOC 2 control kit — the bundles that reference the kinds)

Both merged. (Slice 065 is also merged; its `test-self-host-bundle` job is the end-to-end verification surface for AC-4.)

## Anti-criteria (P0 — block merge)

- Does NOT strip the `.v<major>` suffix from `DefaultSeed()`, the schema files' `x-evidence-kind`, the connectors, or the SDK — those are already on the canonical convention; the fix aligns the SOC 2 bundles TO them, never the reverse.
- Does NOT symptom-patch by double-registering both `osquery.host_posture` AND `osquery.host_posture.v1` — fix the convention drift, do not paper over it.
- Does NOT skip the drift-guard test (AC-5) — the inconsistency was silent for ~14 slices precisely because nothing asserted it.
- Does NOT skip the slice-065 e2e verification (AC-4) — the self-host bundle's phase-6 must demonstrably pass, not just the unit-level registry check.

## Skill mix (3–5)

- Go — schema registry + control-bundle validation path + `cmd/atlas` boot sequencing
- Root-cause analysis (which direction the identifier convention should align — grill against the canvas; and the CI-driven pass that found the boot-time cache race + distroless `/health` probe bug)
- Test design (the mutual-consistency drift-guard across schemas / seed / control bundles)
- docker-compose self-host bundle verification (slice 065's `test-self-host-bundle.sh` — including its startup-ordering interaction with `cmd/atlas`)
