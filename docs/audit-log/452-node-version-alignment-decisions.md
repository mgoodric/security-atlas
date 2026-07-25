# Slice 452 — Node version alignment (→ Node 22 LTS) — decisions log

**Slice:** `docs/issues/452-node-version-alignment.md`
**Type:** JUDGMENT (LTS-line choice) — but the JUDGMENT is **pre-made by the maintainer**.
**Build-then-hold:** P0-452-6 (no auto-merge) — the PR is left OPEN for a one-click
maintainer confirm; the loop builds it targeting the maintainer-decided 22.

---

## Maintainer decision (the JUDGMENT)

> **MAINTAINER DECISION (2026-06-12): target Node 22 ("Jod", Active LTS).**
> Recorded in the slice-doc MAINTAINER DECISION block (lines 9-16). This resolves
> the P0-452-6 JUDGMENT — the implementing agent does NOT pick the line, it builds
> to the decided line.

### D1 — Why Node 22 (not 24, not 25/26)

| Candidate | Status at build (2026-06-12)         | Verdict                                                   |
| --------- | ------------------------------------ | --------------------------------------------------------- |
| Node 20   | Maintenance LTS, EOL ~2026-04        | **Reject** — at/near EOL; the drift we are closing.       |
| Node 22   | **Active LTS** ("Jod"), EOL ~2027-04 | **CHOSEN** — matches current container base (slice 450).  |
| Node 24   | Active LTS, longer remaining window  | Defensible, but more churn; not the maintainer's call.    |
| Node 25   | Odd / **non-LTS** line               | **Reject** — P0-452-1 (the `@types/node ^25` bug itself). |
| Node 26   | **Not yet LTS** at build time        | **Reject** — P0-452-1 (dependabot #153's target).         |

- **Confidence: high.** Node 22 is the conservative, lowest-churn alignment: the
  self-host container base was ALREADY `node:22-alpine` (slice 450), so choosing 22
  makes every surface agree with the runtime that already ships, rather than dragging
  the runtime forward.
- **EOL math:** Node 22 entered Active LTS 2024-10; Maintenance begins ~2025-10;
  End-of-Life ~2027-04-30 (Node.js release schedule). At build time (2026-06) it is
  within its supported window with ~10 months of Active-LTS / Maintenance runway.
- Honors **P0-452-1** (no non-LTS / EOL line) and supersedes dependabot **#637**
  (which would move `@types/node` to non-LTS 25) and **#153** (non-LTS `node:26`).

### D2 — Refresh-cadence note (this WILL drift again)

Like the board-model-refresh note in CLAUDE.md, the Node LTS target is a **documented
maintenance task, not a standing slice**. Cadence:

- **Re-evaluate the Node LTS target every 6 months** (aligned to the Node.js April/October
  release cadence), and **always** when the chosen line crosses from Active LTS into
  Maintenance (Node 22 → Maintenance ~2025-10 already passed; the next decision point is
  the ~2027-04 EOL, at which 24 or 26-by-then-LTS becomes the forward target).
- The refresh moves all four surfaces together (D3) in one slice — never one surface in
  isolation (that is the drift this slice closed; it is why dependabot #637 and #153 are
  superseded rather than merged).

---

## D3 — The four surfaces aligned (all to Node 22, one PR)

| #   | Surface                              | Before           | After            | Note                                                                   |
| --- | ------------------------------------ | ---------------- | ---------------- | ---------------------------------------------------------------------- |
| 1   | `web/package.json` `@types/node`     | `^25`            | `^22`            | The dependabot-#637 bug: odd, non-LTS type-defs line. Now matches 22.  |
| 2   | `web/package.json` `engines.node`    | `>=20`           | `>=22`           | Floor raised to the runtime line.                                      |
| 3   | `.github/workflows/ci.yml` node pins | `"20"` ×8        | `"22"` ×8        | All 8 `setup-node` pins. `grep -c '"20"'` 8→0; `"22"` 0→8.             |
| 4   | `deploy/docker/web.Dockerfile` base  | `node:22-alpine` | `node:22-alpine` | **Already aligned** by slice 450 (3 stages + line-8 comment). No edit. |

- **Surface 4 needed no change.** A repo-wide `grep -rn 'node:20'` over `deploy/` +
  `.github/` returned **no matches** — there is no `node:20` base anywhere. The container
  surface was already at the chosen LTS, so this slice converges the other three onto it.
- Honors **P0-452-2** (all surfaces move together — they now all read Node 22) and
  **P0-452-3** (the 3-stage Dockerfile topology is untouched; no `FROM`-base edit was even
  needed).

## D4 — `@types/node` 25 → 22 downshift: zero forced code change

- **Finding (AC-9 / P0-452-5): NONE.** A `@types/node` major **down**-shift can in
  principle remove/alter type signatures the codebase relies on. It did not here.
- `npm run typecheck -w web` — which runs **both** the slice-450 split configs
  (`tsc --noEmit` over prod `tsconfig.json` AND `tsc --noEmit -p tsconfig.test.json`) —
  exits **0** with **zero** new type errors under `@types/node@22.19.21`. No type was
  corrected, no signature loosened, no suppression added.
- **Resolution detail:** the `web` workspace's **direct** `@types/node` resolves to
  `22.19.21` (a nested `web/node_modules/@types/node` copy + the vitest/vite subtree all
  dedupe to 22.x). A transitive `@types/node@25.8.0` remains, pulled ONLY by
  `shadcn → msw → @inquirer/*` (a dev-time CLI subtree, not in the typecheck graph);
  TypeScript resolves the closest 22.x copy for the `web` source set. This is expected npm
  workspace hoisting and is not a `web`-runtime concern.
- **Confidence: high** — the typecheck is non-vacuous (it compiles 447 `web/` files,
  `strict` on, both configs) and the build + container build re-prove it independently.

## D5 — Self-host verification (the slice-450 lesson)

The CI node bump (20→22) means CI runners + the self-host bundle jobs now build/run on the
aligned config. Verified the self-host web image **for real** (docker available locally):

- `docker build -f deploy/docker/web.Dockerfile -t sa-web-test-452 .` → builder stage
  `✓ Compiled successfully in 5.7s`, image exported. Build exit **0**.
- The build runs `npm ci` from the committed (updated) `package-lock.json` against
  `node:22-alpine` — so it exercises the new lockfile on the runtime base, not just my
  local Node 24.
- **Runtime stage proven, not just reasoned:** ran the image, `node --version` inside =
  **v22.22.3**; the standalone server reported `✓ Ready` and served `GET /login` →
  **HTTP 200**. AC-7 satisfied at runtime.
- The CI self-host bundle jobs (now on Node 22) are the real gate and are watched on the PR.

## D6 — Local CI-parity verification (before push)

| Gate                                      | Result                                            |
| ----------------------------------------- | ------------------------------------------------- |
| `npm install` (repo root)                 | clean; lockfile updated; `@types/node` → 22.19.21 |
| `npm run typecheck -w web` (both configs) | exit 0, zero errors                               |
| `npm run lint -w web`                     | exit 0 (2 pre-existing warnings, not in my diff)  |
| `npm run test -w web` (vitest)            | 184 files / 1760 tests pass                       |
| `npm run build -w web` (next build)       | `✓ Compiled successfully`                         |
| `docker build … web.Dockerfile`           | exit 0, runtime serves HTTP 200 on Node v22.22.3  |
| `grep -c 'node-version: "20"'` ci.yml     | 0 (was 8)                                         |
| `grep -c 'node-version: "22"'` ci.yml     | 8                                                 |

---

## Detection-tier classification

- **`detection_tier_actual`: none** — no bug surfaced during the slice. The
  `@types/node` 25→22 downshift compiled cleanly; the self-host build passed first try.
- **`detection_tier_target`: integration** — had the downshift broken a Node API type or
  the lockfile failed on `node:22-alpine`, the typecheck (unit-adjacent) and the self-host
  container build (integration) were positioned to catch it before merge. The build-vs-
  runtime skew this slice closes is precisely the class the four-surface gate alone cannot
  catch (CLAUDE.md testing discipline) — alignment makes the same Node version gate CI and
  runtime.

## Scope discipline

Version alignment ONLY. No Next.js / React / build-config change (P0-452-4); no Dockerfile
restructuring (P0-452-3; no Dockerfile edit at all); no new CI job. The committed diff is:
`web/package.json` (2 lines), `package-lock.json`, `.github/workflows/ci.yml` (8 pins),
`CHANGELOG.md` (1 bullet), this decisions log.
