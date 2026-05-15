# 080 — Fix release-tag infrastructure (GoReleaser + mkdocs publish) — decisions log

Slice 080 is `Type: AFK`. This log records the subjective build-time
judgment calls + the diagnosis chain that resolved the two release-tag
workflow failures. Format mirrors the JUDGMENT-slice convention
(Diagnosis · Decisions made · Revisit once in use · Confidence).

The slice doc spelled out two named option sets (cosign Path A-1/A-2/A-3
and mkdocs Path B-1/B-2/B-3). **Reading the actual run logs falsified
both hypotheses.** The chosen fixes are not on either menu; the decisions
log records why.

## Diagnosis chain

### Surface A — `GoReleaser · build · sign · publish`

**Slice-doc hypothesis:** `sigstore/cosign-installer@v3` setup is broken;
the action cannot validate `cosign-release: v2.4.1`; the error message
quoted in the slice doc was `Unable to validate cosign version: 'v2.4.1'`
+ `Fetched public key does not match expected digest, exiting` +
`unsupported OS Linux`. Hypothesised fix paths:

- **A-1:** Bump `cosign-installer` to a newer version.
- **A-2:** Pin to a different cosign release.
- **A-3:** Drop cosign signing.

**What the v1.5.1 GoReleaser run log (`25934259652`) actually shows:**

1. `sigstore/cosign-installer@v3` ran. The job log echoes the action's
   install-script source for traceability. That source includes
   `log_error "unsupported OS Linux"` and `log_error "unsupported os
   Linux"` strings — but as **string literals inside the unreached
   branches of the shell script**, not as runtime error output. The
   cosign install step exited successfully and cosign was on PATH.
2. `anchore/sbom-action/download-syft@v0` ran. Successful.
3. The `GoReleaser check` step (which runs `goreleaser check` directly
   in `bash -e`) executed next and failed with:

   ```text
   /home/runner/work/_temp/.../sh: line 1: goreleaser: command not found
   ##[error]Process completed with exit code 127.
   ```

4. The `Run GoReleaser` step (which uses `goreleaser/goreleaser-action@v7`
   — the only step that puts the goreleaser binary on PATH) never ran
   because the preceding `goreleaser check` step failed.

**Actual root cause:** workflow-step ordering. The pre-install
`goreleaser check` step was authored to provide a fast-fail signal on
config errors, but it was placed **before** the only step that installs
the goreleaser CLI. The check has always failed; the cosign references
in the surrounding diagnostic noise are red herrings.

**Why the slice doc's hypothesis missed this:** the cosign-installer
action's stdout includes the (echoed) install-script source as ANSI-colored
lines. Skim-reading the log surfaces the scary strings (`unsupported OS
Linux`, `Fetched public key`) before the actual error line, and the
slice doc was written from that skim. The grill validated by reading the
log past the install-script source-dump.

### Surface B — `Docs publish · Build (mkdocs --strict)`

**Slice-doc hypothesis:** a tar extraction step in setup-uv / cache
restore / mkdocs-material install is corrupt. Hypothesised fix paths:

- **B-1:** Bump `astral-sh/setup-uv`.
- **B-2:** Disable cache restore.
- **B-3:** Install mkdocs-material via pip directly.

**What the v1.5.1 Docs publish run log (`25934259678`) actually shows:**

1. `astral-sh/setup-uv@v7` installed uv 0.11.14 successfully.
2. Cache restore succeeded (`Cache hit` then `Cache restored
   successfully`).
3. The mkdocs build step ran. It downloaded mkdocs, mkdocs-material,
   babel, pygments, installed 34 packages, and built the site:

   ```text
   INFO    -  Cleaning site directory
   INFO    -  Building documentation to directory: /home/runner/work/security-atlas/security-atlas/docs-site/site
   INFO    -  Documentation built in 0.34 seconds
   ```

4. The `Upload Pages artifact` step (`actions/upload-pages-artifact@v5`)
   ran with `path: site` and immediately failed:

   ```text
   tar: site: Cannot open: No such file or directory
   tar: Error is not recoverable: exiting now
   ##[error]Process completed with exit code 2.
   ```

**Actual root cause:** path mismatch. mkdocs resolves a relative
`site_dir` (default `site`) against the config-file directory, so
`mkdocs build --config-file docs-site/mkdocs.yml` writes the rendered
site to `docs-site/site/`. The artifact step was configured with
`path: site` (workspace-root relative). The upload-pages-artifact action
internally tars `$INPUT_PATH`, so its tar invocation looks for `./site`
and fails. The mkdocs build is healthy; the tar is healthy; the path
parameter is wrong.

**Why the slice doc's hypothesis missed this:** the slice doc latched
onto the word "tar" in the error and assumed an extraction step
(setup-uv / cache restore). The actual failure was the **upload-pages**
step's tar invocation on a path that didn't exist, which surfaces with
the same `tar:` prefix.

## Decisions made

### 1. Surface A — remove the pre-install `goreleaser check` step

**Options considered:**

- **(A-novel)** Remove the redundant `goreleaser check` step.
  `goreleaser/goreleaser-action@v7` installs the CLI and runs
  `release --clean`, which validates the config before building. The
  separate fast-fail check was duplicative even before it was broken.
- **(A-novel-alt-1)** Reorder: move `goreleaser check` to run after the
  `goreleaser-action` step. Useless — the action that installs the CLI
  also runs the full release; a check after that point is dead code.
- **(A-novel-alt-2)** Install the goreleaser CLI in a separate first
  step, then run `goreleaser check`, then run the action. Adds CI
  minutes for no benefit; the action already validates.
- **(A-1 / A-2 / A-3 from slice doc)** All rejected: the root cause is
  not in cosign-installer or the cosign release artifact. Touching
  those paths would change nothing.

**Chosen: A-novel.** Smallest fix that addresses the actual root cause.
Removes 7 lines (the step block) from `release.yml`. Honors P0-A2: the
cosign signing path stays exactly as authored — `signs:` in
`.goreleaser.yaml` and the `Install cosign` step in `release.yml` are
untouched. Honors P0-A4: no `continue-on-error`.

### 2. Surface B — fix the upload-pages-artifact path

**Options considered:**

- **(B-novel)** Change `path: site` to `path: docs-site/site` in the
  artifact-upload step. Smallest possible fix. Two-line change (path +
  load-bearing comment).
- **(B-novel-alt)** Override mkdocs's `site_dir` in `mkdocs.yml` to an
  absolute path or to `../site` so the rendered site lands at
  workspace-root `site/`. Rejected: changes a documented mkdocs
  convention (config-relative `site_dir`) to work around a one-line
  artifact-path mismatch in the workflow. The workflow is where the
  mismatch was introduced; the workflow is where the fix belongs.
- **(B-1 / B-2 / B-3 from slice doc)** All rejected: setup-uv, cache
  restore, and mkdocs-material packaging are all healthy. The build
  log proves it. Touching those paths would not help.

**Chosen: B-novel.** Honors P0-A4: no `continue-on-error`.

### 3. Cosign signing path — **kept** (honors P0-A2)

The slice doc enumerated "drop cosign signing" as fix Path A-3. P0-A2
explicitly requires that path only be taken with documented rationale.
**It was not needed and was not taken.** The cosign signing chain
(`Install cosign` step in `release.yml` + the `signs:` block in
`.goreleaser.yaml`) is unchanged. The audit-binding property of release
artifacts — Sigstore keyless OIDC signing of the checksums file, with
Rekor transparency-log entry — is preserved.

### 4. Test-tag verification

**Tag chosen:** `v0.0.0-slice080-test` (per slice-doc suggestion).

Test-tag rationale: tag a no-op commit that triggers both workflows
exactly as a real release-please tag would. Observe both jobs flip
green. Delete the tag locally + on the remote. The tag is not part of
the release-please manifest and does not affect release-please's
proposed-version state (release-please ignores tags that don't match
its semver-only pattern; `v0.0.0-slice080-test` is parsed as a prerelease
and excluded). No GitHub Release object is created because the GoReleaser
`prerelease: auto` config will skip a tag with a `-slice080-test`
suffix... **wait — actually GoReleaser would still cut a prerelease
GitHub Release object for this tag.** The maintainer deletes the
GitHub Release object alongside the tag in cleanup. Process documented
in the new `docs/RELEASE_READINESS.md` §11 "Verifying a release shipped"
subsection.

Backup plan if the test-tag approach surfaces an unexpected interaction:
fall back to `workflow_dispatch`-triggered re-run of both workflows
against the `infra/080-fix-release-tag-infrastructure` branch tip. But
test-tag is the cleaner end-to-end proof.

### 5. AC-5 (P0-A1) — no retroactive re-tag of v1.5.0 / v1.5.1

**Honored.** v1.5.0 and v1.5.1 are tags-of-record. The missing artifacts
stay missing as a historical observation, documented in §11 of
`RELEASE_READINESS.md`. The next real release tag (whatever release-please
opens next — likely v1.5.2 patch or v1.6.0 if a feat commit lands) will
be the first tag with green release-tag CI.

## Revisit once in use

- **`goreleaser check` as a pre-flight** — if a future regression slips
  past `goreleaser/goreleaser-action@v7`'s built-in validation, consider
  reinstating a dedicated check step, but only after a step that
  installs the CLI (e.g., a manual `curl | sh` install). Probably not
  worth the CI minutes; today's removal is the simpler default.
- **Pages publish target convention** — if mkdocs is ever moved out of
  the `docs-site/` subdirectory (e.g., to a sibling repo or to repo
  root), the `path: docs-site/site` value goes stale. The load-bearing
  comment in `docs-publish.yml` documents the coupling so a future
  refactor surfaces the fix-up.
- **Required-check elevation** — both workflows remain tag-only-triggered
  and remain absent from `required_status_checks.contexts`. The slice
  doc explicitly excluded elevating either to a PR-time required check
  (P0-A3: do NOT add a release-pre-flight PR gate). If the maintainer
  ever wants release-tag CI to be visible at PR-merge time, a follow-on
  slice would (1) split the GoReleaser build into a dry-run that runs on
  PRs and (2) split the mkdocs build into a non-strict PR-time check.
  Out of scope for 080.

## Confidence

- **Surface A diagnosis**: HIGH. The exit-127 line is unambiguous and
  reproducible — `bash: goreleaser: command not found` is a
  PATH-resolution failure, not a cosign failure. Step ordering in the
  workflow file confirms the goreleaser CLI is only installed by the
  `Run GoReleaser` step that comes after the failing `goreleaser check`.
- **Surface A fix**: HIGH. The fix removes a redundant step that has
  never worked; `goreleaser-action@v7` validates the config inside its
  own `release --clean` invocation. No behavioural regression possible.
- **Surface B diagnosis**: HIGH. mkdocs's `INFO - Documentation built
  in 0.34 seconds` line + the tar `Cannot open: No such file or
  directory` line on `path: site` are mechanically definitive.
- **Surface B fix**: HIGH. `docs-site/site` is the path mkdocs literally
  printed in the previous log line as its build target.
- **Test-tag plan**: MEDIUM-HIGH. The `v0.0.0-slice080-test` tag triggers
  both workflows exactly the way real release-please tags do. Failure
  modes considered: (a) GoReleaser cuts a prerelease GitHub Release —
  handled by maintainer deleting the Release in cleanup; (b) the tag
  shows up in `git log --tags` clutter — mitigated by deleting locally
  + remote in cleanup.

## Sources

- `release.yml` failed run for v1.5.1: `25934259652`. Job: `GoReleaser ·
  build · sign · publish`. Failing step: `GoReleaser check`. Exit code
  127. Failure line: `goreleaser: command not found`.
- `docs-publish.yml` failed run for v1.5.1: `25934259678`. Job:
  `Build (mkdocs --strict)`. Failing step: `Upload Pages artifact`. Exit
  code 2. Failure line: `tar: site: Cannot open: No such file or
  directory`.
- Same failure pattern on `25922538725` (release v1.5.0) and
  `25898793554` (release v1.4.0) for Surface A, and on `25922538717` /
  `25898793507` for Surface B. The fixes are not specific to the v1.5.1
  tag; they apply to every tagged release that has run since the
  release-tag workflows landed.
- `actions/upload-pages-artifact@v5` source — its `Archive artifact`
  step is a `tar -cvf` over the configured `path:` input, hence the
  tar error surface when `path:` is wrong:
  https://github.com/actions/upload-pages-artifact
- mkdocs `site_dir` resolution: https://www.mkdocs.org/user-guide/configuration/#site_dir
  ("If a relative path is given, it is resolved relative to the config
  file"). The config file lives at `docs-site/mkdocs.yml`, so default
  `site_dir: site` resolves to `docs-site/site`.
