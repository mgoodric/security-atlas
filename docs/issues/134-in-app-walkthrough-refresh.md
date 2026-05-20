# 134 — Refresh slice-070 onboarding walkthroughs against current main (HITL)

**Cluster:** Docs
**Estimate:** 0.5-1d (interactive operator time)
**Type:** HITL — requires live multi-container stack + manual `uvx showboat exec` replay
**Status:** `not-ready` (gate: maintainer schedules interactive session)

## Narrative

**RE-SCOPED 2026-05-19** from the original "in-app walkthrough refresh" framing. The original slice doc described browser-driven in-app product tours — a feature concept that **does not exist** in the codebase. Diagnostic by slice 134's first-pick engineer (escalated): no `tour | driver.js | reactour | intro.js | product-tour` library is integrated; no `web/public/walkthroughs/` directory exists; the only walkthrough-named concepts are slice 027 (auditor evidence recorder — different) and slice 070 (showboat onboarding markdown docs).

The artifact that **has actually drifted** and warrants refresh is the slice-070 onboarding walkthroughs at `docs/walkthroughs/` (5 files: `evaluation-pipeline.md`, `audit-period-freezing.md`, `rls-tenant-isolation.md`, `schema-registry-seed-and-validate.md`, `oscal-ssp-export.md`). Captured 2026-05-16; since then 30+ intervening slices (058 / 070-077 / 091-130 / 117 / 127 / 128 / 132 etc.) have touched paths + signatures the walkthroughs reference. Engineer audit found **24 stale `cd /Users/gmoney/Development/security-atlas-070` path occurrences** across the 5 bash-block sequences plus 1 in a captured output block.

**WHY this is HITL not AFK.** Slice 070's **P0-A4 constitutional anti-criterion** says: "Does NOT manually hand-author the captured output blocks. Every output block in every walkthrough MUST be the actual `showboat exec` capture from a live local run." A clean refresh requires:

1. Bring up the full slice-037 self-host docker-compose stack (`just self-host-up` + `bootstrap.sh` — Postgres + MinIO + NATS + atlas backend + frontend + oscal-bridge)
2. Apply `fixtures/walkthroughs/*.sql` in order (`just walkthroughs-refresh` automates this)
3. **Manually replay every bash block via `uvx showboat exec`** against the live stack (~50 blocks across 5 walkthroughs)
4. Re-sync `docs-site/docs/walkthroughs/` from `docs/walkthroughs/`

Step 3 is interactive multi-hour operator workflow. The continuous-batch loop is AFK-shaped; this slice is intentionally HITL.

**WHAT this slice ships:**

- 5 slice-070 walkthroughs refreshed against `main` HEAD as-of refresh date
- All bash blocks updated (24+ path occurrences corrected to workdir-neutral phrasing)
- All captured-output blocks re-captured (NOT hand-authored, per P0-A4)
- `docs-site/docs/walkthroughs/` re-synced from `docs/walkthroughs/`
- Per-walkthrough frontmatter updated with `last_refreshed_at` + `main_sha_captured_against`

**SCOPE DISCIPLINE — what's deliberately out:**

- Does NOT add any new walkthroughs. The 5 existing files are the contract.
- Does NOT introduce in-app product tours. That was the misframing of the original 134 doc; if the maintainer later wants a browser-driven product-tour feature, file a NEW design slice.
- Does NOT modify the slice-070 P0-A4 anti-criterion. Outputs must be live captures.
- Does NOT modify the `just walkthroughs-refresh` recipe or `uvx showboat` toolchain.
- Does NOT pre-bake the live stack into CI.

## Threat model

Pure docs refresh — minimal threat surface.

**S — Spoofing.** None.

**T — Tampering.** P0-A4 IS the integrity guarantee. Hand-authored outputs would let an attacker craft misleading walkthroughs. The interactive `showboat exec` replay is the trust root.

**R — Repudiation.** None.

**I — Information disclosure.** Walkthroughs run against the local stack with `fixtures/walkthroughs/*.sql` test data — no real tenant data. Captured outputs MUST be reviewed before commit for stray real-data leakage.

**D — Denial of service.** None.

**E — Elevation of privilege.** None.

**Verdict.** has-mitigations (T anchored to P0-A4 + I anchored to test-fixture-only data).

## Acceptance criteria

### Refresh execution (HITL — maintainer-operated)

- **AC-1.** Self-host stack is up locally via `just self-host-up` + `bootstrap.sh`. All services healthy.
- **AC-2.** Fixtures applied via `just walkthroughs-refresh` (steps 1-2 of recipe).
- **AC-3.** All 5 walkthroughs replayed via `uvx showboat exec`. Operator confirms each block produces the expected output; any block that ERRORs gets diagnosed before proceeding.
- **AC-4.** All `cd /Users/gmoney/Development/security-atlas-070` references replaced with workdir-neutral phrasing (e.g., `cd "$(git rev-parse --show-toplevel)"` OR prose-only). Engineer/operator picks convention in decisions log.

### Sync + verify

- **AC-5.** `docs-site/docs/walkthroughs/` byte-identical to `docs/walkthroughs/` after refresh.
- **AC-6.** Each walkthrough's frontmatter records: `last_refreshed_at: <ISO date>`, `main_sha_captured_against: <git rev-parse HEAD>`.
- **AC-7.** `mkdocs build` succeeds with zero warnings.

### Documentation

- **AC-8.** Decisions log at `docs/audit-log/134-walkthrough-refresh-decisions.md`: D1 (workdir-neutral path phrasing), D2 (any prose-only changes due to CLI flag drift), D3 (any walkthroughs broken-by-design — feature removed/renamed; surface as spillover).
- **AC-9.** CHANGELOG entry under `[Unreleased] / Changed`: "Slice-070 onboarding walkthroughs refreshed against main HEAD `<SHA>` (#134)."

## Constitutional invariants honored

- **Slice 070's P0-A4**: captured outputs are live `showboat exec` runs, not hand-authored. **THE LOAD-BEARING invariant for this slice.**
- **CLAUDE.md "Manual evidence is first-class"**: walkthroughs are operator first-touch documentation.

## Canvas references

- Slice 058 (mkdocs scaffold), 070 (showboat content)
- `justfile` `walkthroughs-refresh` recipe

## Dependencies

- **#070** (showboat walkthroughs v1) — `merged`. Slice 134 refreshes 070's output.
- **#058** (mkdocs scaffold) — `merged`.

## Anti-criteria (P0 — block merge)

- **P0-A1.** Does NOT hand-author captured-output blocks. (Inherits slice 070 P0-A4.)
- **P0-A2.** Does NOT add new walkthroughs. 5 files in, 5 files out.
- **P0-A3.** Does NOT modify the `just walkthroughs-refresh` recipe or `uvx showboat` toolchain.
- **P0-A4.** Does NOT skip the `docs-site/docs/walkthroughs/` re-sync. Byte-identical to `docs/walkthroughs/`.
- **P0-A5.** Does NOT introduce real-tenant data in captures. Test-fixture data only.
- **P0-A6.** Does NOT bypass `mkdocs build` warnings.
- **P0-A7.** Does NOT skip frontmatter freshness markers.
- **P0-A8.** Neutral test tokens.

## Skill mix (3-5)

1. **Maintainer (operator)** — primary; runs the interactive showboat replay
2. **Engineer** (optional) — pairs with operator for markdown integration + frontmatter + re-sync + decisions log

## Notes for the implementing agent

**This slice is HITL. The continuous-batch loop CANNOT pick this up unattended.** When the maintainer schedules an interactive session:

1. cd into `/Users/gmoney/Development/security-atlas`
2. `just self-host-up && ./bootstrap.sh` — wait for all services healthy
3. `just walkthroughs-refresh` — applies fixtures (steps 1-2 of recipe automation)
4. For each of the 5 walkthroughs: `uvx showboat exec docs/walkthroughs/<file>.md` (or in-place mode if supported)
5. Verify each block's output; diff against existing markdown; commit refreshed canonical files
6. `cp -r docs/walkthroughs/* docs-site/docs/walkthroughs/` — re-sync
7. `mkdocs build` — verify zero warnings
8. Update each walkthrough's frontmatter
9. Decisions log + CHANGELOG + PR

**Spillover candidates** (file as separate slices if surfaced during refresh):

- **CI advisory check** for slice-070 walkthrough freshness (AFK-shape) — flags walkthroughs whose `last_refreshed_at` is older than N days. Companion piece to make future drift visible without requiring full refresh.
- **Broken-by-design walkthroughs** — if any of the 5 references a CLI/feature that was renamed or removed in a refactor, file as separate prose-rewrite slice.

**Provenance.** Re-scoped 2026-05-19 from "in-app walkthrough refresh" framing. Original engineer escalated cleanly (no commits) when they identified the spec described browser-driven product tours which don't exist. Maintainer chose option B (re-scope doc); this rewrite reflects the actual artifact that has drifted.
