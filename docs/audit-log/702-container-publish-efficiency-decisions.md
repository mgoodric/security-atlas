# Slice 702 — container-publish edge-build efficiency — decisions log

**Slice:** [`docs/issues/702-ci-container-publish-edge-efficiency.md`](../issues/702-ci-container-publish-edge-efficiency.md)
**Branch:** `open-engine/OE-372-security-atlas-702-container-p`
**Date:** 2026-07-24
**Type:** JUDGMENT (CI workflow; no Go/TS code change)

- detection_tier_actual: none
- detection_tier_target: none

No bug surfaced during the slice. The one defect-shaped thing that did
surface — a YAML plain-scalar parse failure on the new `if:` expression
— was caught by `actionlint` (the slice 158 pre-commit guard) before any
commit, which is the tier that is supposed to catch it. It is recorded
in D4 rather than as a detection-tier miss.

---

## Summary

The slice doc was written against a workflow that no longer exists. Two
of its three premises had already been closed by work that landed after
it was filed. This log reconciles the stale findings against `main`,
records the one live inefficiency and the change that closes it, and
records why the third option was declined rather than taken.

| Slice AC | Original finding                                                          | Status now                                                                            |
| -------- | ------------------------------------------------------------------------- | ------------------------------------------------------------------------------------- |
| AC-1     | Docs/status-only `main` merges rebuild all 8 image variants               | **Already fixed** — `paths-ignore:` (2026-06-15, PR #1346). Verified empirically, D1. |
| AC-2     | A release merge triggers TWO full builds for the same SHA                 | **Fixed by this slice** — release-commit skip on the `build` job, D3.                 |
| AC-3     | Release images keep multi-arch + signing + provenance                     | **Untouched** — D5.                                                                   |
| AC-4     | Option (b) amd64-only edge needs an unconsumed-arm64 confirmation first   | **Declined, not deferred-silently** — D6.                                             |
| —        | "arm64-via-QEMU is the slowest path in the CI estate" (narrative premise) | **Already obsolete** — QEMU removed, D2.                                              |

---

## D1 — AC-1 was already satisfied; verified rather than assumed

**Decision:** Close AC-1 as already-fixed. No workflow change.

The slice doc claims docs-only merges rebuild all 8 image variants.
That was true when it was filed. It is not true on `main`.

**File/line evidence.** `.github/workflows/container-publish.yml` lines
87-90 (all line numbers in this log are against the file as it stands
after this slice's edit) carry:

```yaml
paths-ignore:
  - "Plans/**"
  - "docs/**"
  - "**/*.md"
```

introduced by `62c04dc4 ci(container-publish): native-arm64 build split

- edge debounce (#1346)`, with the volume-control rationale in the
  comment at lines 80-86.

**Why I did not stop at reading the config.** `paths-ignore` skips a run
only when EVERY changed path matches — the slice doc's own "Do" step 2
flags this, and a filter that looks right can still miss (a top-level
`README.md` against a `**/*.md` pattern is the classic doubt: does `**`
match zero directory segments?). So I checked the observed behaviour
instead of the intent. Four docs-only merges on `main`, and the
container-publish runs GitHub actually created for each:

| Commit     | Changed paths                                                                                                | container-publish runs |
| ---------- | ------------------------------------------------------------------------------------------------------------ | ---------------------- |
| `c7a7bad5` | `CHANGELOG.md`, `docs/adr/0010-*.md`, `docs/adr/0016-*.md`, `docs/issues/368-*.md`                           | none                   |
| `ec9d0332` | `docs/issues/747-*.md`, `docs/issues/_events.jsonl`                                                          | none                   |
| `1487b734` | `docs/issues/_STATUS.md`                                                                                     | none                   |
| `8a4c8d4e` | `CHANGELOG.md`, `README.md`, `docs-site/docs/{configuration,oauth-grants,oidc-setup}.md`, `docs/releases.md` | none                   |

(`gh run list --workflow=container-publish.yml --commit=<sha>` returned
an empty set for all four.)

Two things worth naming beyond a bare "it works":

- `8a4c8d4e` settles the `**/*.md` question empirically: a **top-level**
  `README.md` and `CHANGELOG.md` are matched, so `**` does match zero
  segments here. A filter written as `*.md` + `**/*.md` would have been
  the defensive form; it is not needed.
- `ec9d0332` and `1487b734` are the `_events.jsonl` / `_STATUS.md` cases.
  Neither is a `.md` file — `_events.jsonl` is JSONL — but both live
  under `docs/`, so the `docs/**` pattern covers them. This is the
  `chore(status)` fraction the slice doc specifically called out.

**Residual gap, named honestly:** the filter is path-based, so a
docs-only change OUTSIDE `Plans/`, `docs/`, and `*.md` still builds.
`.claude/skills/**` is the live example in this repo. This is not worth
another pattern — the surface is small and the failure direction is a
wasted build, not a missing one. Recorded so the next person does not
re-derive it.

## D2 — the QEMU premise is obsolete; the slice's cost model was wrong

**Decision:** Treat the slice's "arm64-via-QEMU is 4-8x native and the
slowest path in the whole CI estate" framing as historical, and do NOT
let it drive the option ranking.

`container-publish.yml` lines 41-64 record the migration: the workflow
built `linux/amd64,linux/arm64` in a single `ubuntu-latest` job under
QEMU user-mode emulation, which intermittently died with
`qemu: uncaught target signal 4 (Illegal instruction)` during `next
build` (the SWC native binary is Rust). It now builds each arch on its
own native runner — `ubuntu-latest` for amd64, `ubuntu-24.04-arm` for
arm64 (lines 187-193) — pushes each per-arch image by digest (line 240),
and assembles the tagged manifest list in a separate `merge` job via
`docker buildx imagetools create` (lines 349-351). Landed in the same
PR #1346.

This matters for the option ranking, not just as trivia. The slice doc
ranks option (b) (amd64-only edge) as a meaningful speed win because it
assumed the arm64 leg cost 4-8x the amd64 leg. With both legs on native
runners and running in parallel across a matrix, dropping the arm64 leg
no longer shortens the critical path — the two legs are concurrent, so
wall-clock is `max(amd64, arm64)`, not `amd64 + 4x·amd64`. Option (b)
would buy runner-minutes, not latency. See D6.

The `concurrency:` block (lines 104-121) also already provides the edge
debounce, with its slice 451 AC-5 rationale for why dropping a
superseded `:main-<sha7>` is acceptable. That precedent is load-bearing
for D3.

## D3 — AC-2: skip the edge build on the release commit (the live fix)

**Decision:** Implement option (c). Gate the `build` job on the pushed
commit not being release-please's release commit.

**The inefficiency is real and measured.** For each of the last two
releases, two container-publish runs exist for the identical SHA:

| SHA                  | push run                  | release run               | gap |
| -------------------- | ------------------------- | ------------------------- | --- |
| `7b7725a4` (v1.17.0) | `26956746345` @ 14:03:25Z | `26956764391` @ 14:03:43Z | 18s |
| `411151b9` (v1.16.0) | `26365658466` @ 15:46:17Z | `26365663828` @ 15:46:30Z | 13s |

The push run builds 8 image variants tagged `:edge` + `:main-<sha7>`;
the release run builds the same 8 from the same tree and tags them
`vX.Y.Z` / `X.Y` / `X` / `latest`. The edge leg is redundant.

**Why `paths-ignore` does not already catch this.** The release-please
release commit changes exactly two files — verified with `git show
--stat` on the last three:

```
.release-please-manifest.json |   2 +-
CHANGELOG.md                  | 148 ++++++++++++++++++++++++++++++++++++++++++
```

`CHANGELOG.md` matches `**/*.md`, but `.release-please-manifest.json`
does not match any ignore pattern — and `paths-ignore` skips only when
ALL changed paths match. So one unmatched file re-arms the whole edge
build. Adding `.release-please-manifest.json` to `paths-ignore` was
considered and rejected: it would be a coincidental fix (it works only
because that file happens to be the sole non-`.md` path today) and it
would silently stop working the moment release-please's `release-type:
go` starts stamping a version file. The condition should say what it
means.

**Why a commit-message match and not a tag lookup.** The obvious
implementation is "skip if a `v*` tag points at `github.sha`". That is a
race, and the table above is the proof: the tag does not exist when the
push run starts — the 13-18s gap IS release-please cutting it. A tag
lookup would evaluate false on every release and the guard would never
fire, which is the worst kind of bug (silently inert, looks correct in
review). release-please's release commit subject is deterministic, so:

```yaml
if: "${{ github.event_name != 'push' || !startsWith(github.event.head_commit.message, 'chore(main): release ') }}"
```

**Precision/recall checked against the full history of `main`,** not
argued from the release-please docs:

- 19 commits on `main` have a subject starting with `chore(main): release `.
- All 19 carry exactly one `v*` tag (`v1.0.0` through `v1.17.0`, plus
  `v1.5.1`). 19/19 true positives — no release is missed.
- 0 other commits match. The nearest miss in the history,
  `chore(release-please): surface chore and ci commits in changelog
(#144)`, correctly does NOT match: its prefix is `chore(release-please)`,
  not `chore(main): release `. No false positives.

The trailing space in the literal is load-bearing (it is what makes
`chore(main): release-something` a non-match), and the `(#NNN)` suffix
that squash-merge appends is tolerated because the test is `startsWith`,
not equality.

**Accepted consequences.** Both are stated in the workflow comment so a
future reader does not have to reconstruct them:

1. `:edge` lags by exactly one commit across a release. That commit
   changes only `CHANGELOG.md` and `.release-please-manifest.json`,
   neither of which enters any image — so the lag is a tag pointer
   moving late, not stale image content. The next merge to `main`
   refreshes `:edge`. Watchtower on the edge box polls `:edge` and will
   simply see no new digest until then.
2. No `:main-<sha7>` image is published for the release commit. This is
   the same trade the `cancel-in-progress` debounce already makes (lines
   111-121), under the same slice 451 AC-5 reasoning: the trust anchor
   for an edge image is its provenance attestation, not a guarantee of
   one image per SHA. The identical code ships as `vX.Y.Z` + `latest`
   seconds later.

**Failure direction is safe.** If `head_commit` is ever null, GitHub
coerces it to `''`, `startsWith` returns false, the negation returns
true, and the build RUNS. The guard can waste a build; it cannot
silently skip one.

**Escape hatch.** If release-please merges a release commit and then
fails to cut the tag, neither run happens for that SHA. The pre-existing
`workflow_dispatch` trigger (with its `tag` input) covers it manually.

**Condition placement.** The `if:` lives on `build` only. `merge` has
`needs: build` and the default `if: success()`, so it is skipped
transitively. Duplicating the expression onto `merge` would be
self-documenting but creates a second place to drift; a comment on
`merge` explains the inheritance instead.

## D4 — the `if:` expression must be a quoted YAML scalar

**Decision:** Wrap the expression in double quotes.

First attempt was an unquoted plain scalar. `actionlint` rejected it:

```
.github/workflows/container-publish.yml:167:101: could not parse as YAML:
mapping values are not allowed in this context [syntax-check]
```

The match literal `'chore(main): release '` contains `: `, which YAML
reads as a mapping separator inside a plain scalar. Double-quoting the
whole value (single quotes stay available for the inner strings)
resolves it. Noted because the failure mode is generic: any GitHub
Actions `if:` matching a Conventional Commit subject hits it, and the
error message points at the colon rather than at the quoting.

`actionlint -shellcheck "" -no-color` passes on the final file, and
`yaml.safe_load` round-trips the condition unchanged.

## D5 — release images untouched (anti-criteria audit)

**Decision:** Explicitly verify the anti-criteria rather than assert
them. The diff touches exactly one job's `if:` plus comments.

| Anti-criterion                                    | Evidence                                                                                                                                                                                           |
| ------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Release images keep amd64 + arm64                 | The `platform:` matrix (lines 187-193) is unchanged. The `if:` is false only when `github.event_name == 'push'`; a `release` event short-circuits on the first clause and always builds both legs. |
| Release signing untouched                         | Not in this file. `release.yml`'s cosign keyless step is unmodified — this slice does not touch `release.yml` at all.                                                                              |
| Provenance untouched                              | `provenance: true` + `sbom: true` (lines 260-261) and the `Attest provenance` step in `merge` are unmodified.                                                                                      |
| AC-13 dual-arch assertion still runs              | The `Verify multi-arch manifest` step's `jq -e` check on `linux/amd64` + `linux/arm64` is unmodified and still gates every release.                                                                |
| No QEMU reintroduced, native-runner matrix intact | No change to `runs-on`, the matrix, or the push-by-digest/`imagetools create` split.                                                                                                               |
| No published images deleted                       | This slice publishes fewer images going forward; it deletes none. `edge-image-prune.yml` is untouched.                                                                                             |
| Edge changes only                                 | The only behavioural change is which `push` events build.                                                                                                                                          |

## D6 — option (b) declined, and what would be needed to revisit

**Decision:** Do NOT make the edge channel amd64-only. This is a
decline, not a silent deferral.

Two independent reasons, either sufficient:

1. **The evidence bar was not met.** The slice's AC-4 and the task's
   boundaries both require confirming edge-arm64 is unconsumed FIRST.
   What the repo shows: the edge deploy target is
   `atlas-edge.home.gmoney.sh` running on the maintainer's Unraid host
   (slice 207 decisions log lines 189-191, 287, 293-300), and Unraid is
   x86_64-only — so the one deployment the repo documents consumes
   amd64. But "the documented deployment is amd64" is not "arm64 is
   unconsumed." `deploy/docker/docker-compose.edge.yml` pulls `:edge`
   from GHCR and nothing stops it being run on an arm64 workstation, and
   GHCR does not expose per-architecture pull counts that would settle
   it. Absence of an arm64 consumer in the repo is not evidence of
   absence.
2. **The premise that made it attractive is gone.** Per D2, the two arch
   legs now run concurrently on native runners. Dropping arm64 from edge
   saves runner-minutes but does not shorten the critical path, so the
   thing being bought is much smaller than the slice doc assumed — while
   the cost (a silently arm64-less `:edge`, discovered by whoever next
   runs the edge compose stack on an arm64 machine) is unchanged.

Given (2), the value of resolving (1) is low. Option (c) delivers the
outcome the slice was after. **If it is ever revisited, the question for
the maintainer is narrow: does anything other than the Unraid box pull
`:edge` — in particular, has the edge compose stack ever been run on an
arm64 machine?** A "no" plus a note in `docs/SELF_HOSTING.md` that edge
is amd64-only would clear the bar.

## D7 — first post-revival runs

The workflow was disabled repo-wide 2026-06-29 → 2026-07-24. The most
recent container-publish run is `28393062108` (2026-06-29T18:12:03Z,
push, success) — i.e. **no post-revival run has happened yet**, so there
is no failure signature to report and nothing to block on. The last 15
runs before the decommission were all `success`; the last `release`-event
run (`26956764391`, v1.17.0, 2026-06-04) was a `failure`, which predates
and is unrelated to this change — worth a look on the next release cut,
but it is not this slice's scope and it does not gate the change here.

**What to watch on the next release**, which is where this change first
takes effect: exactly ONE container-publish run should exist for the
release SHA, with `event=release`. Confirm with:

```
gh run list --workflow=container-publish.yml --commit=<release-sha>
```

Before this change that command returned two rows; it should now return
one.

## D8 — explicit non-touches

- `release.yml` — not opened for edit. Release supply chain is out of
  scope by anti-criterion.
- `edge-image-prune.yml` — untouched (separate, currently-disabled
  workflow; boundary says image pruning is not this slice's business).
- `release-please.yml` — untouched. Its own `paths-ignore` (lines 22-28 of that file)
  is a different filter for a different workflow and is correct as-is.
- `paths-ignore` in `container-publish.yml` — deliberately NOT extended
  (see D3 for why `.release-please-manifest.json` was rejected as a
  patch site, and D1 for the `.claude/skills/**` residual).
- `docs/issues/702-*.md` — the slice doc's `Status:` field is left at
  `ready`; `docs/issues/_STATUS.md` is generated (`just status`) and is
  non-gating per CLAUDE.md.
