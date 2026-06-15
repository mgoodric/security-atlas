# Slice 696 — Share the `.next` frontend build + standardize on `npm ci` — decisions log

JUDGMENT slice. Two adjacent frontend-CI inefficiencies from the slice 693
pipeline-efficiency audit (Finding 1.3 / 1.4, Tier 2 / audit Finding 2.2):

1. **Part A — `npm ci` standardization (AC-1).** An unconditional clean win.
2. **Part B — `.next` artifact-sharing (AC-2/AC-3/AC-5).** A BENCHMARK-GATED
   call. The slice explicitly authorizes DROPPING AC-3 with a recorded
   rationale if the artifact round-trip does not clearly beat a fresh build.

The build-time subjective call here is the Part-B adopt/drop decision, made
with measured data per the continuous-batch JUDGMENT convention; the
maintainer iterates post-deployment. This does NOT touch the product-runtime
AI-assist boundary (separate, constitutional).

Source: slice 693 pipeline-efficiency audit, Finding 1.3 / 1.4 / 2.2.
Cross-references: slice 695 (`share-atlas-binary` — the JUST-MERGED sibling
whose `build-atlas` dedicated-job re-shape is the measure-first discipline
applied here; its decisions log D3 is the canonical "do not gate a fast job
behind a slow producer" lesson), slice 279 (the upload/download-artifact
coverage pattern), slice 387 (the `build:standalone` prod-build job kept
untouched per AC-4).

- detection_tier_actual: none
- detection_tier_target: none

(No bug surfaced during the slice. Part A is a deterministic command swap; Part
B's verification is the benchmark below. The converted `npm ci` steps run on
this PR itself because a `.github/workflows/**` edit trips the `code`
paths-filter, so every converted job exercises `npm ci` end-to-end on the PR.)

---

## D1 — Part A: convert all six `npm install --no-audit --no-fund` → `npm ci` (ADOPTED, unconditional)

Six CI frontend steps used `npm install --no-audit --no-fund`
(`build-frontend` L914, `frontend-vitest` L1332, `frontend-lint` L1389,
`frontend-playwright` L1568, `frontend-playwright-prod-build` L1815,
`frontend-ui-honesty` L1980); only `npm-audit` (L2678) already used `npm ci`.
All six are converted to `npm ci --no-audit --no-fund`.

Why `npm ci` is strictly better in CI:

- **Lockfile-faithful + deterministic** — installs the exact tree
  `package-lock.json` pins; never silently mutates the lockfile (the
  correctness win the audit Finding flagged).
- **Faster** — skips the resolver pass; does a clean `node_modules` install
  from the lockfile.
- **Fails loud on drift** — if `package-lock.json` is out of sync with
  `package.json`, `npm ci` errors instead of papering over it.

**Lockfile sync precondition — VERIFIED, no sync needed.** `npm ci` only works
against an in-sync lockfile, so I ran `npm ci --no-audit --no-fund` in the
worktree root before relying on it:

```
added 625 packages in 5s
```

It succeeded and left `package-lock.json` byte-identical (`git status` clean
afterward). There is NO lockfile drift, so NO `package-lock.json` change ships
in this PR — the swap is the entire Part-A change. (Had there been genuine
drift, the fix would have been a committed re-synced lockfile; there wasn't, so
regenerating gratuitously was correctly avoided.)

The diff is exactly six single-line edits. No job `name:`, no `needs:`, no test
assertion, no `-stub` twin, and no Node pin (`node-version: "22"` everywhere)
is touched.

**Confidence: high.**

## D2 — Part B: `.next` artifact-sharing — MEASURED, then DROPPED (AC-3 dropped; AC-2 not adopted)

The slice (AC-5) requires benchmarking the artifact round-trip vs. a fresh
`next build` and adopting sharing ONLY if the round-trip clearly wins. I did
not trust the a-priori estimate — I measured the build and the artifact, and
the data plus the job-graph structure point to DROP.

### The measurement

Local benchmark (warm npm cache, fast Mac — relative shape, not CI absolute):

| Quantity                                  | Measured                                                 |
| ----------------------------------------- | -------------------------------------------------------- |
| Fresh `npm run build` (`next build`)      | **~11 s local** (~60–150 s on a CI runner per slice doc) |
| `.next` total size                        | **97 MB**                                                |
| `.next` file count                        | **6,158 files**                                          |
| `.next` tar+gzip                          | ~21 MB (6 s to create)                                   |
| `.next` plain tar (≈ upload-artifact zip) | ~95 MB (7 s to create)                                   |

`web/next.config.ts` sets `output: "standalone"` as the project DEFAULT, so
even the bare `next build` (build-frontend / frontend-playwright /
frontend-ui-honesty) emits the full `.next/standalone` tree (52 MB of the
97 MB). The artifact is heavy regardless of build mode.

### Why the round-trip does NOT clearly win

`actions/upload-artifact@v7` zips the directory per-file. The cost is
**file-count-dominated** at 6,158 files (thousands of tiny chunks under
`.next/server` + `.next/static`), not bytes-dominated:

- **Upload (producer, once):** zip 6,158 files + transfer ~21–95 MB to the
  artifact store ≈ **30–90 s**.
- **Download + unzip (each of 2 consumers):** ≈ **20–60 s EACH**.

That replaces a `next build` the slice doc itself bands at ~60–150 s on CI —
i.e. the round-trip lands in the SAME band as the thing it replaces, and
plausibly exceeds it once you count two downloads. There is no clear win to
bank.

### The decisive structural reason — it re-introduces the slice 695 anti-pattern

Today `frontend-playwright`, `frontend-playwright-prod-build`, and
`frontend-ui-honesty` depend on `needs: [changes, build-atlas]` ONLY. They each
run `next build` **in parallel with** `build-frontend` (nothing depends on
`build-frontend` except the merge-gate result-check). To adopt `.next` sharing,
`build-frontend` (or a new dedicated build-web producer) would have to become a
`needs:` of the two standard-build consumers — **serializing two
currently-parallel jobs behind a producer that itself runs `next build` + the
sdk build + the new upload**. That is exactly the wall-clock regression slice
695 MEASURED under `needs: build-go` and re-shaped away from (695 decisions D3:
"do not gate a fast job behind a slow producer"). Here the producer is not even
fast — it runs its own Next build.

And `next build` is not the long pole in the consuming jobs anyway: each also
pays `npx playwright install --with-deps chromium` plus the full Postgres +
NATS + MinIO bring-up — the multi-minute critical path. Shaving an overlapping
~60–150 s build only to add serialized artifact I/O + a new serialization edge
is a net-negative trade.

### The call: DROP AC-3 (and therefore do not adopt AC-2)

Per AC-3's own escape hatch ("ONLY if the round-trip benchmarks faster than a
fresh build; otherwise this AC is dropped with a recorded rationale") and the
anti-criterion ("does NOT adopt artifact-sharing blind to the size/round-trip
cost"), the correct outcome is to ship Part A and DROP Part B. Shipping a
documented "artifact-sharing not worth it" benchmark is, per the slice brief, a
complete and correct outcome. AC-2 is consequently not adopted (it exists only
to feed AC-3). AC-4 (prod-build keeps `build:standalone`) and AC-6 (Node pin +
suites green) hold trivially because no build step or pin changed.

**Confidence: high** (the round-trip cost is file-count-dominated and lands in
the build's own time band; the serialization edge is the same mechanism slice
695 proved regressive).

## D3 — Producer choice (moot, recorded for the revisit)

Had Part B been adopted, the right shape — mirroring slice 695's `build-atlas`
— would have been a dedicated lightweight build-web producer that does ONLY
`npm ci` + `next build` + upload, with the two standard-build consumers
`needs:`-ing it, NOT folding the upload into `build-frontend` (which also builds
the sdk workspace and would drag that onto the consumers' critical path).
Because Part B is dropped, no producer is introduced; this is recorded so a
future revisit starts from the correct shape rather than the `build-frontend`
trap.

---

## Revisit once in use

- **Re-measure if `.next`'s file count or the artifact-store throughput
  changes materially.** The DROP rests on 6,158 files making the zip/unzip
  round-trip land in the `next build` time band. If a future Next version
  drastically shrinks the chunk count (fewer, larger files), the round-trip
  math could flip — re-run the benchmark before reconsidering AC-3.
- **If Part B is ever revisited, use a dedicated build-web producer (D3), not
  `build-frontend`** — and re-measure the job-graph critical path on a real PR
  run (the slice 695 discipline), because adding a `needs:` edge to two
  currently-parallel jobs is the regression risk.
- **`npm ci` floor is now uniform** — every frontend CI job is lockfile-faithful.
  A future dep bump that forgets to update `package-lock.json` will now fail
  CI loudly (the intended correctness ratchet) rather than silently mutating
  the lockfile under `npm install`.
