# 068 — Schema-registry evidence_kind identifier fix

**Cluster:** evidence-pipeline
**Estimate:** 1-1.5d
**Type:** AFK

## Narrative

Fix a pre-existing evidence_kind identifier inconsistency in the schema registry that breaks fresh-deploy control-bundle upload. Surfaced by slice 065's AC-12 self-host end-to-end CI job: once the self-host bundle boots cleanly (all 33 migrations + seed + atlas startup), bootstrap phase 6 — "upload control bundles from `/repo/controls/soc2`" — fails with:

```
error: upload failed: HTTP 400: control bundle: evidence_kind "osquery.host_posture" is not registered in the schema registry
```

### The bug

The repo disagrees with itself about how an evidence_kind is identified:

- **`DefaultSeed()`** in `internal/api/schemaregistry/registry.go` registers kinds with a **`.v1` suffix** baked into the `Kind` string — `{Kind: "osquery.host_posture.v1", Version: "1.0.0"}` — i.e. the `.v1` AND a separate `Version: "1.0.0"`.
- **The schema directories** are bare: `internal/api/schemaregistry/schemas/osquery.host_posture/1.0.0.json` — kind `osquery.host_posture`, version `1.0.0`.
- **The SOC2 control bundles** (`controls/soc2/*/control.yaml`, slice 010) reference the **bare** kind: `osquery.host_posture`.
- **The evidence-push path** (the Evidence SDK `IngestEvidence` contract) uses bare kind + separate semver.

So on a fresh deploy, atlas's registry has `osquery.host_posture.v1` but the control bundle asks for `osquery.host_posture` — lookup fails. This affects **every bare-named evidence_kind the SOC2 bundles reference** (`access_review.completion`, `sast.scan_result`, `policy.acknowledgment`, `github.audit_event`, the `okta.*` kinds, …); `osquery.host_posture` is just the first bundle the bootstrap hits.

The bug has been latent because nothing exercised "fresh bootstrap → upload control bundles" end-to-end until slice 065's AC-12 job. **It means fresh-deploy control-bundle upload is broken on `main` today** — slice 037's "4-hour-to-first-evidence" demo path does not actually work from a clean checkout.

The fix is a small, bounded alignment — but it must be root-caused, not symptom-patched: confirm whether atlas seeds its registry from the slim `DefaultSeed()` fallback or the file-backed loader at boot, then make the evidence_kind identifier convention consistent repo-wide (bare kind name + separate semver — the convention the schema dirs, control bundles, and evidence-push path already use). The slice delivers value because it unbreaks the self-host bundle's last broken phase and greens slice 065's AC-12 end-to-end job.

## Acceptance criteria

- [ ] AC-1: `DefaultSeed()` in `internal/api/schemaregistry/registry.go` returns bare `Kind` strings (no `.v1` suffix) — `{Kind: "osquery.host_posture", Version: "1.0.0"}` etc. — matching the `schemas/*/` directory names and the `controls/soc2/*/control.yaml` references for every kind.
- [ ] AC-2: Root-cause and fix how the atlas server seeds its schema registry at boot. Confirm it loads the full file-backed set from `internal/api/schemaregistry/schemas/*/` (not just the slim `DefaultSeed()` fallback, which the docstring says exists "for unit tests that don't want to spin up the file loader"). If the docker-bundle atlas was falling back to `DefaultSeed()`, fix the seeding path so a real deployment registers every shipped schema.
- [ ] AC-3: A fresh-deploy bootstrap's phase-6 control-bundle upload succeeds — every evidence_kind referenced by every `controls/soc2/*/control.yaml` resolves in the registry. Verify by enumerating the distinct evidence_kinds across all SOC2 bundles and asserting each is registered.
- [ ] AC-4: Slice 065's `test-self-host-bundle` CI matrix job passes in **both** modes (bundled + external) — this is the end-to-end proof. (Slice 065's AC-12 has been red pending this slice; 068 is what greens it.)
- [ ] AC-5: A drift-guard test: assert that the set of `schemas/*/` directory kinds, the set of `DefaultSeed()` kinds, and the set of evidence_kinds referenced by `controls/soc2/*` are mutually consistent (every shipped schema is seeded; every bundle-referenced kind has a schema). This prevents silent recurrence.
- [ ] AC-6: Audit the other evidence_kind consumers — the Evidence SDK push path, the connectors (`connectors/*`) that emit evidence — for the same `.v1` mismatch. Confirm they already use the bare convention, or fix any that don't. Document the canonical convention (bare kind + separate semver) in a short comment at the `DefaultSeed()` / registry definition site.
- [ ] AC-7: `CHANGELOG.md` entry under `[Unreleased]/Added` (or `Fixed`).

## Constitutional invariants honored

- **Invariant 3 (one canonical inbound Evidence API):** the evidence_kind identifier is part of the `IngestEvidence` contract — this slice makes the registry agree with the contract the push path and connectors already honor.
- **Working norms — cite sources:** the canonical convention gets a comment at the definition site so the next contributor doesn't reintroduce the `.v1` drift.

## Canvas references

- `Plans/canvas/04-evidence-engine.md` (Evidence SDK + evidence_kind schemas)
- `Plans/EVIDENCE_SDK.md` (the `IngestEvidence` contract — evidence_kind identifier shape)
- `docs/issues/014-schema-registry-service.md` (the schema registry this slice corrects)
- `docs/issues/010-soc2-control-kit.md` (the control bundles whose evidence_kind references must resolve)

## Dependencies

- **014** (schema registry service — `DefaultSeed()` + the file loader live here)
- **010** (SCF-anchored SOC 2 control kit — the bundles that reference the kinds)

Both merged. (Slice 065 is also merged; its `test-self-host-bundle` job is the end-to-end verification surface for AC-4.)

## Anti-criteria (P0 — block merge)

- Does NOT symptom-patch by registering both `osquery.host_posture` AND `osquery.host_posture.v1` — fix the convention, don't double-register.
- Does NOT change the on-the-wire evidence_kind identifiers that connectors/pushers already emit — those use the bare convention; the fix aligns `DefaultSeed()` TO that convention, never the reverse.
- Does NOT skip the drift-guard test (AC-5) — the inconsistency was silent for ~14 slices precisely because nothing asserted it.
- Does NOT skip the slice-065 e2e verification (AC-4) — the self-host bundle's phase-6 must demonstrably pass, not just the unit-level registry check.

## Skill mix (3–5)

- Go — schema registry seeding + the file loader path
- Root-cause analysis (boot-time registry seeding: `DefaultSeed()` fallback vs file loader)
- Test design (the mutual-consistency drift-guard across schemas / seed / control bundles)
- docker-compose self-host bundle verification (slice 065's `test-self-host-bundle.sh`)
