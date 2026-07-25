# Slice 703 — main-canary: one representative leg on docs-only `main` pushes · decisions log

**Type:** JUDGMENT · **Approach:** implement (single-leg on `changes.code == 'false'`), NOT WONTFIX · **Date:** 2026-07-24

- detection_tier_actual: none
- detection_tier_target: none

> No defect surfaced while building this slice. The change is a CI-topology
> edit verified by `actionlint` plus the expression-semantics table in D4; there
> was no bug to classify.

## What changed

`tests-integration-main-canary` in `.github/workflows/ci.yml`:

|                                                       | before                           | after                                                                       |
| ----------------------------------------------------- | -------------------------------- | --------------------------------------------------------------------------- |
| trigger                                               | `push` + `refs/heads/main`       | unchanged                                                                   |
| `needs:`                                              | (none)                           | `changes`, guarded by `always()`                                            |
| leg matrix                                            | `[A, B1, B2, B3, B4, B5]` always | `["A"]` when `changes.code == 'false'`; `[A, B1, B2, B3, B4, B5]` otherwise |
| `cancel-in-progress`                                  | `false`                          | unchanged (`false`)                                                         |
| concurrency group                                     | `ci-main-canary-<sha>-<leg>`     | unchanged                                                                   |
| `-race`                                               | yes                              | unchanged                                                                   |
| steps / services / `scripts/run-integration-shard.sh` | —                                | unchanged                                                                   |

Nothing on the PR path moved. `scripts/run-integration-shard.sh` and
`scripts/integration-shards.txt` are untouched; the canary still consumes the
slice-417 manifest as the single source of truth.

**Spec-vs-repo note.** Slice 703's narrative and the OE both say "four-leg
matrix". The matrix has been SIX legs since slice 747 (B4 + B5 were added).
"Full matrix" is implemented as the current six-leg list, not a literal four,
so the anti-criterion "does NOT reduce coverage on code-touching `main` pushes"
holds against the repo as it actually is. The saving is ~5/6 of the canary's
runner-minutes on a docs-only push, not the ~3/4 the spec estimated.

---

## D1 — The masking-window analysis (the load-bearing decision; AC-4)

This is the analysis the spec required BEFORE any change. It holds, so the
slice is implemented rather than closed WONTFIX.

### The hole, restated precisely

From slice 631's decisions log, the slice-474 merge-safety hole is: **an
in-repo code change reached `main` and no COMPLETED integration-shard run ever
existed for it**, so "green `main`" did not imply "green shard". Two masking
mechanisms compounded:

1. **Path-filter SKIP.** On a docs/status-only commit the real shard legs are
   skipped (slice-061 stub-twins post the green required checks), so that SHA
   has no completed shard run.
2. **Concurrency CANCEL.** The workflow-level `ci-${{ github.ref }}` group with
   `cancel-in-progress: true` lets the next `main` push cancel the previous
   `main` run before its leg finishes — `cancelled`, never RED-and-completed.

The canary (mechanism (c)) is the after-merge net: unconditional + uncancellable
so that a completed shard run exists on every `main` SHA.

### Why one leg on a docs-only push does not reopen it

**Step 1 — a docs-only `main` SHA cannot contain a code change.**
`changes.code == 'false'` means the push diff matched none of the filter's
patterns. That list is not narrow: it covers `**/*.go`, `internal/**`,
`cmd/**`, `pkg/**`, `migrations/**`, `sql/**`, `proto/**`, `policies/**`,
`schemas/**`, `fixtures/**`, `connectors/**`, `web/**`, `oscal-bridge/**`,
`deploy/**`, `go.mod`/`go.sum`/`go.work`/`go.work.sum`, `justfile`,
`.golangci.yml`, Dockerfiles, `docker-compose*.yml`, **`scripts/**`** and
**`.github/workflows/**`**.

The last two are the ones that make this argument airtight: the shard runner
(`scripts/run-integration-shard.sh`), the shard manifest
(`scripts/integration-shards.txt`) and the canary job definition itself are all
inside the `code` filter. So on a docs-only SHA, **every input to the
integration-shard outcome — the tests, the code under test, the package→leg
assignment, the service images, the job definition, the dependency graph — is
byte-identical to its parent `main` SHA.** The only things that can have moved
are markdown, plan docs, `LICENSE`, `CHANGELOG`, `docs/**`.

**Step 2 — induct backwards over the `main` lineage.**
Let `S` be a docs-only `main` SHA. Its parent `P` is either:

- **code-touching** → `P`'s own canary ran the FULL leg matrix,
  uncancellably (`cancel-in-progress: false`, per-SHA-per-leg group), with
  `-race` and real services. Every leg has a completed run against exactly the
  code state that `S` carries; or
- **docs-only** → recurse on `P`.

The recursion is finite and terminates at the most recent code-touching `main`
SHA. Therefore, **for any docs-only SHA, a completed full-matrix run against
its exact code state already exists on `main`.** The docs-only canary carries
ZERO marginal information about the code. It is a _re-sample_ of an
already-sampled code state, and the breadth of a re-sample is a cost dial, not
a correctness gate.

**Step 3 — mechanism (2) is closed at the code SHA, not at the docs SHA.**
The cancel mechanism is defeated by `cancel-in-progress: false` on the canary's
own per-SHA-per-leg concurrency group. That protection lives on the
code-touching push's canary run, which this slice does not touch. The docs-only
canary was never the thing closing mechanism (2); a rapid docs merge cannot
cancel the earlier code SHA's canary legs, which is precisely what slice 631
engineered.

**Step 4 — mechanism (1) is what the docs-only canary addresses, and it is
vacuous for the code question.** Mechanism (1) masks a regression when a
regression exists on a SHA whose legs were skipped. By Step 1 a docs-only SHA
introduces no regression; by Step 2 its code state is already covered. The
docs-only canary's job is therefore not "catch a regression this push
introduced" (impossible) but "keep the `main`-SHA record populated".

**Step 5 — the literal slice-631 guarantee survives verbatim.** The guarantee
text is "a completed shard run exists on this `main` SHA". A single leg is a
completed, uncancellable, `-race`, real-services shard run on that SHA — full
Postgres/MinIO/NATS bring-up, `migrations/bootstrap/01-roles.sql`, the full
forward-migration sweep, `-p 1`. The guarantee's _existence_ clause is
preserved exactly; only its _breadth_ narrows, and only on SHAs where breadth
provably buys no code information.

### The residual risk, named honestly

There is exactly one thing the six-leg docs-only canary catches that the
one-leg version does not: a **leg-B_i-specific failure whose cause is outside
the repo** — an upstream image tag moving under a floating reference
(`postgres:16-alpine`, `minio/minio` unpinned, `nats:2.10-alpine`), a GitHub
runner-image rollout, a Go 1.26 patch release — or a genuine flake in a Phase B
package. For such a cause, the detection window widens from "next `main` push
of any kind" to "next code-touching `main` push".

Three reasons that is an acceptable trade, not a reopening of slice 474:

1. **It is a different defect class.** Slice 474 was a deterministic _in-repo_
   regression (a host-clock-dependent round-trip). The canary was built for
   "an in-repo regression merged and never got a completed run", and Steps 1–4
   close that class completely. Environmental drift was never the canary's
   stated purpose — it is a side benefit of running the matrix often.
2. **The window is bounded by the next code merge, which is the window every
   other consumer already lives with.** Nothing else in the pipeline
   re-validates Phase B legs on a cadence faster than "the next Go PR", and the
   PR-time matrix + merge-gate block that PR closed.
3. **The chosen leg is the one most exposed to that residual risk** — see D2.

### If this analysis had failed

The WONTFIX trigger would have been Step 1 or Step 2 breaking — e.g. if
`scripts/**` or `.github/workflows/**` were NOT in the `code` filter (then a
docs-only SHA could change the shard runner or the job definition and the
induction would collapse), or if a leg's outcome depended on repository state
outside the filter. Both were checked against `.github/workflows/ci.yml` lines
73–115. They hold.

---

## D2 — Leg A is the representative leg (justified on coverage, not speed)

Leg A is deliberately **not** the cheapest leg. Slice 747 moved
`./internal/api/controls/...` (~228s) OFF Leg A precisely because A was the
critical-path pole, and A retains an irreducible floor (~47s seed/migration
setup + the ~128s order-independence guard) that no Phase B leg carries. Picking
A is a coverage-over-speed call, which is what the spec asked for.

What Leg A uniquely or best covers:

- **The only leg that seeds the shared global SCF catalog.** The P0-2 pinned
  cluster (`scf_anchors` with no `tenant_id`, plus the `evidence_kind_schemas`
  schema-registry rows) is seeded ONLY on Leg A. No Phase B leg exercises the
  global catalog-seed path at all — pick any B leg and that path goes untested
  on docs-only SHAs entirely.
- **The migration + tenancy foundation.** `./internal/db/...` and
  `./internal/dbtest/...` cover the migration surface and the RLS core
  (constitutional invariant #6 — tenant isolation enforced at the database
  layer). Leg A also carries the slice-461 order-independence guard and the
  migration round-trip.
- **The most environment-sensitive packages in the repo.**
  `./internal/backup/...` creates and drops ephemeral databases on the cluster
  and dumps the whole DB — the package with the tightest coupling to the actual
  `postgres:16-alpine` image. `./internal/evidence/ingest/...` and
  `./internal/evidence/streambuf/...` exercise NATS JetStream. Since D1's named
  residual risk is _environmental drift in the pinned service images_, the leg
  that maximises sensitivity to that drift is the right single sample.
- **Both halves of constitutional invariant #2.** Ingestion
  (`internal/evidence/ingest`, `streambuf` — the append-only ledger) and
  evaluation (`internal/eval/...`, `internal/control`, `internal/decision/...`,
  `internal/api/controlstate/...`) both live on A.
- **Structural argument.** Read the manifest's own assignment rule: B1 is the
  auth/admin handler family, B2 is audit/board/metrics/policy/risk/vendor, B3
  and B5 are the OSCAL import cluster, B4 is `api/controls` plus cross-cutting
  read handlers. Every Phase B leg is one _handler family_. Leg A is the
  _platform foundation those families sit on_. A break in A's surface breaks
  everything above it; a break in B2's surface is local to B2. If exactly one
  leg may run, the foundation is the leg with the widest blast radius covered.

Rejected alternatives: B4 (fastest-to-value but it is `api/controls` plus read
handlers — pure application surface, no migration/catalog/ledger coverage); a
rotating leg keyed off `github.run_number` (gives long-run breadth but makes any
single docs-only SHA's coverage nondeterministic and unreviewable, and the
per-SHA guarantee is the whole point); the cheapest leg by wall-clock (explicitly
the criterion the spec ruled out).

---

## D3 — `needs: changes` is guarded by `always()`

Adding `needs: changes` introduces a dependency the canary did not have. A
plain `needs:` would mean a failed or cancelled `changes` job SKIPS the canary
— a new flavour of the exact masking mechanism the canary exists to close, and
`changes` sits in the workflow-level `ci-${{ github.ref }}` group with
`cancel-in-progress: true`, so it is genuinely cancellable.

`if: always() && github.event_name == 'push' && github.ref == 'refs/heads/main'`
restores the pre-703 property: the canary runs on every `main` push regardless
of upstream job state. The branch/event guards are unchanged, so it still never
appears on the PR path.

## D4 — Fail-safe direction: test `== 'false'`, not `!= 'true'`

The spec's AC-1 says "one leg when `changes.code != 'true'`". Implemented as:

```yaml
leg: ${{ fromJSON(needs.changes.outputs.code == 'false' && '["A"]' || '["A","B1","B2","B3","B4","B5"]') }}
```

`dorny/paths-filter` emits exactly the strings `'true'` or `'false'`, so for
every real run this is observably identical to `!= 'true'`. It differs only in
the degenerate case D3 made reachable:

| `needs.changes.outputs.code`    | `!= 'true'` would give | this expression gives |
| ------------------------------- | ---------------------- | --------------------- |
| `'true'` (code push)            | full matrix            | full matrix           |
| `'false'` (docs-only push)      | `["A"]`                | `["A"]`               |
| `''` (changes failed/cancelled) | `["A"]`                | **full matrix**       |

Coverage must never narrow on an _unknown_ signal, only on an affirmative
docs-only one. The `fromJSON(<string ternary>)` shape (ternary over strings,
`fromJSON` applied once to the result) is used rather than
`<cond> && fromJSON(...) || fromJSON(...)` because the latter relies on an
array being truthy in the GitHub expression language; string truthiness is
unambiguous.

## D5 — This change cannot affect any merge decision

Stated for the record, because the canary sits near branch protection:

- The canary is push-to-`main` only (`github.event_name == 'push'`), so it never
  produces a check on a PR and never enters the merge-blocking critical path.
- Branch protection names the aggregate `Go · integration (Postgres RLS)`
  fan-in and `CI · merge-gate` — never the per-leg
  `Go · integration canary (main, shard ...)` contexts (slice 417 AC-11 /
  747 AC-3). No branch-protection change is needed or implied.
- No job `needs:` the canary; it is a leaf.
- The PR-time `tests-integration-shard` matrix, the `tests-integration` fan-in
  and `CI · merge-gate` are all untouched.

## Verification

- `actionlint -shellcheck "" -no-color .github/workflows/ci.yml` → exit 0
  (the slice-158 D3 pre-commit guard; catches the workflow-drops-at-parse class).
- YAML parse + job introspection confirms: `needs: changes`, `if` retains both
  `push` and `refs/heads/main` guards, `concurrency.cancel-in-progress: false`
  and the `ci-main-canary-<sha>-<leg>` group unchanged, all 10 steps unchanged,
  `-race` still passed to `scripts/run-integration-shard.sh`.
- Runtime observation of both branches requires merges to `main` of each kind;
  the maintainer can confirm on the next docs-only merge that exactly one
  `Go · integration canary (main, shard A)` check appears.

## Anti-criteria check

| Anti-criterion                                               | Status                                                                                   |
| ------------------------------------------------------------ | ---------------------------------------------------------------------------------------- |
| Does NOT reduce coverage on code-touching `main` pushes      | Held — full (six-leg) matrix, unchanged, on `code == 'true'`; also the fail-safe default |
| Does NOT make the canary cancellable                         | Held — `cancel-in-progress: false` and the group key are byte-identical                  |
| Does NOT move the canary onto the PR path                    | Held — `github.event_name == 'push' && github.ref == 'refs/heads/main'` unchanged        |
| Does NOT proceed without the masking-window analysis         | Held — D1, written before the edit                                                       |
| Does NOT change `scripts/run-integration-shard.sh` semantics | Held — file untouched; so is `scripts/integration-shards.txt`                            |
