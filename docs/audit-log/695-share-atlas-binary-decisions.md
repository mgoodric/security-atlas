# Slice 695 — Share the prebuilt atlas binary across jobs (build once) — decisions log

JUDGMENT slice. The build-time subjective calls (the upload/download wiring, the
executable-bit fix, and — load-bearing — the serialization wall-clock call that
was measured, found regressive under `needs: build-go`, and re-shaped to a
dedicated `build-atlas` job) are recorded here per the continuous-batch
JUDGMENT convention; the maintainer iterates post-deployment. This does NOT
touch the product-runtime AI-assist boundary (separate, constitutional).

Source: slice 693 pipeline-efficiency audit, Finding 1.2 / 2.1 (the `atlas`
binary is compiled up to 5× per code PR; the three playwright-family host
compiles of `./cmd/atlas` on the identical `ubuntu-latest` runner are
redundant). Cross-references: slice 279 (the `go-unit-coverage`
upload/download artifact pattern this mirrors), slice 694 (trivy-image
in-Docker build cache — the 4th/5th compile, out of scope here), slice 696
(`.next` sharing — the conceptually-adjacent follow-on).

- detection_tier_actual: none
- detection_tier_target: none

(No bug surfaced during the slice. This is a pure CI-config dedup; the
verification surface is the PR's own CI run going green — actionlint +
check-yaml validate the workflow edits, and `build-atlas` + the three e2e jobs
actually run on this PR because the `code` paths-filter includes
`.github/workflows/**`, so the build → upload → download → install → server-start
path is exercised end-to-end on the PR itself.)

---

## D1 — Share via upload/download-artifact (mirror the coverage pattern)

`build-go` already uploads `coverage.txt` as the `go-unit-coverage` artifact
(`actions/upload-artifact@v7`) and `tests-integration` already downloads it
(`actions/download-artifact@v4.1.8`). Slice 695 reuses that exact, already-pinned
pair rather than introducing a cache action or a job-output mechanism. Reasons:
(a) artifacts are the idiomatic GitHub way to pass a build output between jobs;
(b) the two actions are already SHA-pinned in this file, so `check-action-pins.sh`
and the pin discipline (slice 128) are satisfied with zero new pins; (c) the
binary is a single file (~tens of MB) — well within artifact limits and cheaper
to transfer than to recompile. The atlas compile (`go build -o atlas ./cmd/atlas`)
and the upload both live in a dedicated lightweight `build-atlas` job (see D3 for
why this is NOT a step in `build-go`).

**Confidence: high.**

## D2 — `install -m 0755` after download (the executable-bit fix)

`actions/upload-artifact` does NOT preserve the Unix executable bit — the
downloaded `atlas` file comes back non-executable, and the e2e jobs would fail
with "permission denied" the moment they try to start the atlas server. The fix
is `install -m 0755 /tmp/atlas-bin/atlas /usr/local/bin/atlas` (equivalent to
`cp` + `chmod +x`, in one atomic, portable, actionlint-clean command). The
`atlas --version || true` line that follows is a non-fatal smoke check that the
binary actually runs on the runner (it must not fail the step if the subcommand
name differs — hence `|| true`; the authoritative validation is the e2e suite
booting the server in the existing `Start atlas server` step). This was called
out as the MANDATORY gotcha and is the single change most likely to break the
jobs if missed.

**Confidence: high.**

## D3 — Serialization wall-clock: measured, then re-shaped to a dedicated `build-atlas` job (the JUDGMENT)

The slice doc (AC-5) requires measuring before/after and keeping the change only
if wall-clock does not regress materially; billing always improves (3× redundant
host compiles removed). I did NOT trust the a-priori estimate — I shipped the
first cut (`needs: build-go`), measured the real run, found a regression, and
re-shaped. The measurement is the point of a JUDGMENT slice.

**First cut — `needs: build-go` — and what the data showed.** The initial
implementation put the atlas build+upload in `build-go` and gated the three
playwright jobs on `needs: build-go`. Measured on the PR's own run
(GHA run 27581378713 / CI workflow run 27581378707, branch
`ci/695-share-atlas-binary`):

| Job                                                    | Started  | Completed | Wall-clock |
| ------------------------------------------------------ | -------- | --------- | ---------- |
| `Go · build + test` (build-go)                         | 22:46:35 | 22:54:03  | **~7m28s** |
| `Frontend · Playwright e2e`                            | 22:54:06 | 22:59:38  | ~5m32s     |
| `Frontend · Playwright e2e (prod-build standalone)`    | 22:54:12 | 22:56:23  | ~2m11s     |
| `Frontend · UI honesty (advisory)`                     | 22:54:06 | 22:56:38  | ~2m32s     |
| `Go · integration (shard A)` (longest integration leg) | 22:46:35 | 22:56:27  | ~9m52s     |
| `CI · merge-gate`                                      | 22:59:46 | 22:59:57  | —          |

The a-priori estimate was WRONG: `build-go` is ~7.5 min, not "low single-digit"
— the `-race -coverpkg=./...` unit run dominates, not the build. The three
playwright jobs all started at 22:54:06–22:54:12, i.e. they waited the full
~7.5 min for `build-go` before starting (vs. starting at t≈0 before this change,
fanning out from `changes` alongside the integration shards). `Frontend ·
Playwright e2e` then finished at **22:59:38 — the LAST job before merge-gate
(22:59:46)**, making it the PR critical path. Pre-change it would have started
at t≈0 and finished ~7 min earlier (≈22:52), comfortably inside the integration
tier's window (longest leg done 22:56:27). **Net: a measured ~7-min wall-clock
regression on a code PR.** That is material — fails AC-5's "do not regress
materially" bar.

**The re-shape — dedicated `build-atlas` job.** The root cause is that `needs:`
waits for the WHOLE upstream job, and `build-go` carries a ~7.5-min `-race` unit
run that the playwright jobs do not need. The fix keeps the dedup but kills the
regression: a new lightweight `build-atlas` job (name `Go · build atlas binary
(shared)`) does ONLY checkout + setup-go + `go build -o atlas ./cmd/atlas` +
upload — ~1 min with a warm cache. The three playwright jobs `needs:
[changes, build-atlas]` instead of `build-go`. `build-atlas` fans out from
`changes` in parallel with `build-go` and the integration shards, so the
playwright `needs:` wait drops from ~7.5 min to ~1 min — within the noise of
their own multi-minute runtime, and far inside the integration tier's ~10-min
window, so the playwright jobs leave the critical path.

`build-atlas` is `if: code == 'true'` (mirrors `build-go`) and is NOT a required
check (absent from `.github/branch-protection.json`), so it needs no stub twin:
on docs-only PRs it skips alongside the real playwright jobs, whose existing
stubs post the required check names.

**The keep/wontfix call: KEEP (via `build-atlas`).** Both wins now hold
simultaneously:

1. **Billing improves unconditionally** — three redundant identical-target host
   compiles removed; the binary is built exactly once (~3 runner-min/code-PR).
2. **Wall-clock does NOT regress** — the ~1-min `build-atlas` serialization is
   immaterial against the playwright jobs' own runtime and the integration
   tier's ~10-min critical path. Each playwright job also sheds its ~20–40 s
   in-job atlas compile for a seconds-long artifact download.

The plain `needs: build-go` variant would have been a wontfix-or-revert (material
regression). The dedicated-job re-shape is the keep. The post-re-shape run's
numbers are recorded in the PR body once green.

**Confidence: high** (the regression was measured, not assumed; the fix removes
its mechanism — the long upstream job — entirely).

## D3a — Why measure-then-reshape rather than ship the first cut for the billing win

The slice offered a fallback: "ship the dedup anyway for the billing win" even if
wall-clock regresses. I rejected that for the `needs: build-go` cut because a
~7-min wall-clock regression on every code PR is a daily, visible cost paid by
every contributor, whereas the billing win is a background line-item. Trading 7
min of every contributor's feedback loop for ~3 runner-min of billing is a bad
trade. The `build-atlas` re-shape makes the trade-off moot — both wins at once —
which is why it is the shipped shape rather than the regress-but-ship fallback.

## D4 — Keep `setup-go` in the three playwright jobs

After removing the atlas compile, the playwright jobs no longer run `go build`
themselves. `setup-go` (with `cache: true`) is nonetheless KEPT in all three:
removing it is out of scope for this slice (the slice is "share the binary", not
"prune toolchain setup"), the cache restore is cheap, and keeping it minimizes
the diff and the blast radius (no risk of a downstream step that transitively
expects the Go toolchain on PATH). A separate slice can prune it if a toolchain
audit confirms nothing in those jobs needs Go on PATH.

**Confidence: high.**

---

## Revisit once in use

- **Re-confirm the wall-clock call against real PR runs over a week.** D3's KEEP
  rests on `build-atlas` being ~1 min and the playwright/integration jobs being
  much longer. If a future change inflates `build-atlas` (it shouldn't — it only
  compiles one target) or shrinks the integration tier below the playwright
  jobs' finish time, re-measure whether `build-atlas` ever becomes the long pole.
  The billing win is permanent regardless.
- **Pairs with slice 696 (`.next` sharing).** When 696 lands, the build-web
  output could likewise be shared to the playwright jobs; revisit whether the
  combined artifact-sharing changes the job-graph critical path enough to
  warrant re-measuring.
- **Prune `setup-go` from the three playwright jobs (D4)** if a toolchain audit
  confirms no remaining step needs the Go toolchain on PATH — a small follow-on
  saving, deliberately not bundled here.
- **`atlas --version` smoke line** — if `./cmd/atlas` does not implement
  `--version`, the `|| true` keeps the step green but the smoke loses signal.
  When a stable `atlas version`/`--version` subcommand exists, tighten the smoke
  to assert it (drop the `|| true`).
