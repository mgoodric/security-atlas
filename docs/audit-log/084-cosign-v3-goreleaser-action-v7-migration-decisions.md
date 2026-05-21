# 084 — cosign v3 + goreleaser-action@v7 migration — decisions log

Slice 084 is `Type: AFK` (JUDGMENT-class build-time discretion). This log
records the subjective build-time judgment calls + the args-shape
research that resolved the cosign-v3 sign-blob breakage slice 080 hit at
iteration 5.

Format mirrors the JUDGMENT-slice convention (Diagnosis · Decisions
made · Revisit once in use · Confidence).

## Prerequisite context

Read first: `docs/audit-log/080-fix-release-tag-infrastructure-decisions.md`.
That log captures the full iteration cascade slice 080 hit when bumping
the cosign + goreleaser-action stack mid-flight. The terminal state of
slice 080 (the recovery point this slice picks up from) is:

- `goreleaser/goreleaser-action@v6` (specifically `e435ccd…`) — tactical
  revert from v7 because v7's internal cosign pre-flight verify of its
  own downloaded binary uses cosign v3's protobuf-bundle format that
  cosign v2 cannot read.
- `sigstore/cosign-installer@v3` (specifically `398d4b0…`) with
  `cosign-release: v2.4.1` — known-good pairing from slice 039.
- `.goreleaser.yaml` `signs:` block writing the legacy two-file output
  (`--output-signature` + `--output-certificate`).
- Consumer-side `verify-blob` snippet using `--certificate ... --signature
...` (legacy two-file shape).

Slice 080 deferred the cosign-v3 migration explicitly: "the cosign-v3
cascade is a real migration that needs the new-bundle-format flag set
worked out, the consumer-side `verify-blob` args worked out, and the
published docs (Release-notes verify snippet + Self-verify step) kept in
sync. That deserves its own slice with proper testing budget, not a deep
tail of cascading version bumps inside slice 080."

This slice is that proper testing budget.

## Diagnosis chain

### What cosign v3 actually mandates

Reading the cosign v3.0.x release notes end-to-end resolves the args
question slice 080 left open:

- **v3.0.0 announcement**: "Default to using the new protobuf format
  (#4318)". The Sigstore protobuf bundle (single `.sigstore.json` file
  containing leaf cert + signature + Rekor inclusion proof) becomes the
  default. v2-compat output is opt-in via `--new-bundle-format=false`.
- **v3.0.1 / v3.0.2 release notes**: "Note that the `--bundle` flag
  specifying an output file to write the Sigstore bundle (which contains
  all relevant verification material) has moved from optional to
  required in v3." The `--bundle <path>` flag is now mandatory on
  `sign-blob`.
- **v3.0.3 changelog**: "4554: Closes 4554 - Add warning when `--output*`
  is used (#4556)". The legacy `--output-signature` / `--output-certificate`
  flags emit deprecation warnings on v3.0.3+.
- **v3.0.6 changelog (the version this slice lands on)**: "Disallow
  `--new-bundle-format` and `--rfc3161-timestamp` (#4762)". The
  `--new-bundle-format` boolean toggle that slice 080 iteration 5 tried
  to pass `=false` to is **now rejected outright** as of v3.0.6. The new
  bundle format is the only output mode; there is no v2-compat opt-out.

This resolves slice 080 iteration 5's confusion. The error message
"`must provide --new-bundle-format or --bundle where applicable with
--signing-config or --use-signing-config`" was a v3.0.3-era message;
v3.0.6 simplified by removing the toggle entirely.

### Why slice 080's iteration 4 + 5 failure was inevitable

Slice 080 iteration 4 saw `Error: signing dist/...checksums.txt: create
bundle file: open : no such file or directory`. The empty path between
`open` and `:` is the smoking gun: cosign v3 was trying to write a
bundle to a path that was never set. Our `signs:` args at iteration 4
still used `--output-signature=${signature}` + `--output-certificate=
${certificate}` only — no `--bundle` flag at all. cosign v3 with the
new-bundle-format default needs `--bundle <path>` to know where to write
the bundle file; without that flag it tries to write to an empty string
and crashes.

Slice 080 iteration 5 then tried `--new-bundle-format=false` to opt out
of the new format and keep the legacy two-file output working. That
worked on v3.0.0-v3.0.5 (the flag accepted a boolean value), but the
error message at iteration 5 suggests the goreleaser run was against a
cosign release that already had the disallow patch (v3.0.6+) — or the
flag-parsing changed in a way the slice doesn't make precise. Either
way, the right fix is not to keep fighting for the v2-compat opt-out;
it's to migrate the args + the consumer docs to the v3-native shape.

### What goreleaser-action@v7 expects from its pre-flight verify

goreleaser-action@v7 added an internal cosign-verify step against its
own downloaded `goreleaser` binary. The action's binary is signed in the
new Sigstore protobuf-bundle format, so the verify needs a cosign on
PATH that understands that format. cosign v3.x understands it natively;
cosign v2.x does not. Slice 080's iteration 1 saw the failure mode
(`bundle does not contain cert for verification, please provide public
key`) when goreleaser-action@v7 ran against cosign v2.4.1.

The implication for this slice: bumping goreleaser-action to v7 +
cosign to v3 is **one atomic change**, not two independent bumps. The
two upstreams are co-evolving on the new bundle format.

## Decisions made

### 1. Args shape — cosign v3 sign-blob with `--bundle ${signature}`

**The chosen `signs:` block:**

```yaml
- id: checksums
  cmd: cosign
  args:
    - sign-blob
    - "--yes"
    - "--bundle=${signature}"
    - "${artifact}"
  artifacts: checksum
  output: true
  signature: "${artifact}.sigstore.json"
```

**Why this shape (not alternatives):**

- **(Chosen)** Single `--bundle=${signature}` arg with `signature:`
  template repurposed to name the bundle file (`*.sigstore.json`).
  Cleanest path because goreleaser's release-asset discovery is keyed
  off the `signature:` field — registering the bundle as the
  signature-asset gets it uploaded to the GitHub Release without any
  custom asset-discovery plumbing.
- **(Rejected)** Adding a separate `bundle:` field in the goreleaser
  `signs:` schema — there is no such field in goreleaser v2's schema.
  The `signature:` + `certificate:` are the only filename templates
  the asset-discovery logic understands.
- **(Rejected)** Keeping `certificate:` pointing at a never-written
  `.pem` file — would create a 404 asset on the release. Removed
  outright.
- **(Rejected)** Writing the bundle as `${artifact}.bundle.json` — the
  Sigstore-community convention is `*.sigstore.json` (matches
  `sigstore-go` SDK examples, `sigstore-python` examples, and the
  cosign v2.6.0 release-note examples). Naming consistency with the
  broader ecosystem.

**Confidence: HIGH.** This is the args shape called out by the cosign
v2.6.0 release notes as the v3-default flow:

```
cosign sign-blob --use-signing-config --bundle sigstore.json README.md
```

We drop `--use-signing-config` because it's specific to the new
Sigstore SigningConfig TUF metadata flow (an opt-in service-URL discovery
mechanism); the defaults work for the keyless OIDC + Fulcio + Rekor
combo we're already using.

### 2. Consumer-side `verify-blob` args migration

**The chosen verify shape (Release header + Self-verify step +
RELEASE_READINESS §11.1):**

```sh
cosign verify-blob \
  --certificate-identity-regexp 'https://github.com/mgoodric/security-atlas/\.github/workflows/release\.yml@.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --bundle security-atlas_<version>_checksums.txt.sigstore.json \
  security-atlas_<version>_checksums.txt
```

**Why no `--new-bundle-format` flag on `verify-blob`:**

cosign v3.0.6 commit `2290a593` explicitly disallows
`--new-bundle-format` on both `sign-blob` and `verify-blob`. The new
bundle is the only format the v3 binaries produce or accept; the flag
became redundant. Passing it on v3.0.6 fails with an error; omitting it
is the correct shape for v3.0.6+.

This is one of the AC-4 items: **the `--new-bundle-format` flag is no
longer applicable to v3.0.6.** It was a boolean toggle in cosign v3.0.0-
v3.0.5 but is rejected on v3.0.6+. We don't pass it anywhere in this
slice (neither in `signs:` args nor in `verify-blob` args).

**Confidence: HIGH.** Matches the cosign v3.0.6 explicit-disallow patch

- the cosign v2.6.0 release-note example verify-blob shape (with
  `--new-bundle-format` removed per v3.0.6).

### 3. Version pinning — cosign v3.0.6 + cosign-installer v4.1.2 + goreleaser-action v7.2.2

- `sigstore/cosign-installer@v4.1.2` (SHA
  `6f9f17788090df1f26f669e9d70d6ae9567deba6`). v4.x is the lowest
  installer release that bootstraps cosign v3.x; v3.x of the installer
  bundles a v2-era trust-anchor `public.key` that fails to verify v3.x
  cosign binaries (exit 22). The installer publishes only point
  releases, no floating `@v4` major tag (slice 080 iteration 3
  confirmed: `Unable to resolve action sigstore/cosign-installer@v4`).
  v4.1.2 is the highest v4.x as of 2026-05-20.

- `cosign-release: v3.0.6`. Highest stable v3.x as of 2026-05-20.
  Includes the security fix for GHSA-w6c6-c85g-mmv6 (DSSE predicate
  check) + the `--new-bundle-format` / `--rfc3161-timestamp` flag
  cleanup. Future dependabot will surface v3.0.7+ on a normal bump
  cycle — those are minor patches that should not require args
  rework.

- `goreleaser/goreleaser-action@v7.2.2` (SHA
  `5daf1e915a5f0af01ddbcd89a43b8061ff4f1a89`). Highest v7.x as of
  2026-05-20. v7's internal cosign pre-flight verify expects a
  cosign on PATH that understands the new Sigstore protobuf-bundle
  format — cosign v3.x meets that requirement; v6 used a non-cosign
  verify path and is no longer needed.

### 4. COSIGN_EXPERIMENTAL env var dropped

**Removed** `COSIGN_EXPERIMENTAL: "1"` from the goreleaser-action env
block. The variable was a cosign v1/v2 signal that "I am opting into
the experimental keyless flow"; in cosign v3, keyless OIDC + the new
bundle format are the default and only behavior. The env var is silently
ignored. Removed to avoid documenting intent the tool no longer honors.

### 5. P0-A2 honored — cosign signing chain preserved end-to-end

The slice's P0-A2 ("Does NOT remove cosign signing as the path-of-least-
resistance fix") is honored: the cosign signing chain is preserved
identically, only the on-wire format changes. Audit-binding properties
(Sigstore keyless OIDC signing, Rekor transparency-log entry, signed
checksums file) are all intact. What changed:

- Signature wire shape: two files (`.pem` + `.sig`) → one file
  (`.sigstore.json`)
- Verification command: `cosign verify-blob --certificate <pem>
--signature <sig> <artifact>` → `cosign verify-blob --bundle
<sigstore.json> <artifact>`

Both shapes provide the same security guarantees; the new shape is the
Sigstore ecosystem default going forward.

### 6. Test-tag verification — `v0.0.0-slice084-test`

**Plan:** push a `v0.0.0-slice084-test` tag at the branch tip after
the PR is open; observe the workflow; if green, verify the published
artifacts cosign-verify cleanly from a clean shell; delete the test tag

- GitHub Release object in cleanup.

**Why a test tag (not workflow_dispatch):** the test tag exercises the
exact code path a real release-please tag would — `push:` event with
`tags: v*.*.*` matcher, the `concurrency: release-${{ github.ref }}`
group, the `GITHUB_REF_NAME` interpolation in the Self-verify step.
`workflow_dispatch` would not exercise the tag-driven trigger and
would not produce a GitHub Release object to round-trip-verify against.

**Tag-name choice:** `v0.0.0-slice084-test` matches the slice 080
convention. GoReleaser parses it as a prerelease (3-digit semver +
`-suffix`) and the `prerelease: auto` config will mark the cut GitHub
Release as a prerelease. release-please's manifest is unaffected — it
ignores prerelease tags. Cleanup deletes the tag (local + remote) and
the GitHub Release object alongside; the slice 080 cleanup playbook in
`docs/RELEASE_READINESS.md §11.x` already documents that flow.

## Revisit once in use

- **cosign v3.0.7+ adoption** — dependabot will surface v3.0.7+ on a
  normal cycle. These should be minor patches; no args rework expected.
  If a v3.x patch ever changes the bundle format again, file a follow-on
  slice (same shape as 084) — don't fold the migration into a dependabot
  PR.
- **Sigstore protobuf-bundle filename convention** — `*.sigstore.json`
  is the current convention. If the Sigstore community standardizes on
  a different extension (e.g., `*.sigstore.bundle`), the
  `signature: "${artifact}.sigstore.json"` template in `.goreleaser.yaml`
  needs a coordinated update with the consumer docs.
- **Older releases (pre-slice-084) verify-flow** — releases shipped
  before this slice's cutover tag still have `.pem` + `.sig` assets on
  their GitHub Release. Consumers who need to verify those older
  releases need a cosign v2.x binary (or cosign v3.x + the explicit
  legacy-output flow, if a future cosign v3.x patch reinstates it).
  This is documented as a note in `docs/RELEASE_READINESS.md §11.1`
  rather than spillover-as-slice — the older releases are immutable
  by P0-A1 of slice 080.
- **Trust-anchor rotation cadence** — cosign-installer@v4.x bundles
  trust anchors for the v3 release line. When cosign v4 lands
  (future), expect a cosign-installer@v5 to follow the same pattern.
  Don't pre-emptively bump.

## Confidence

- **Args-shape (sign-blob `--bundle=${signature}`)**: HIGH. Backed by
  cosign v3.0.x release notes, the v2.6.0 announcement example, and
  the v3.0.6 explicit-disallow patch on `--new-bundle-format`.
- **Args-shape (verify-blob `--bundle <file>`)**: HIGH. Same backing.
- **Version pinning (v3.0.6 + v4.1.2 + v7.2.2)**: HIGH. All current
  highest-stable as of 2026-05-20.
- **COSIGN_EXPERIMENTAL drop**: HIGH. Read the cosign v3 source-of-truth
  release notes; the variable is no-op on v3.
- **Test-tag round-trip green**: MEDIUM-HIGH until the test tag actually
  runs. If the round-trip surfaces an unexpected interaction (e.g.,
  goreleaser v2's `signature: ${artifact}.sigstore.json` template
  doesn't get picked up by the asset-discovery logic as expected), the
  fix is likely a small adjustment to the goreleaser `signs:` block
  shape rather than a re-think of the args migration. The migration
  direction (legacy two-file → bundle) is correct; the goreleaser
  plumbing might need iteration.

## Sources

- cosign v3.0.0 announcement / release notes:
  https://github.com/sigstore/cosign/releases/tag/v3.0.0
  ("Default to using the new protobuf format")
- cosign v3.0.1 / v3.0.2 release notes: `--bundle` flag moved from
  optional to required.
- cosign v3.0.3 release notes: "Add warning when `--output*` is used".
- cosign v3.0.5 release notes: "Allow `--local-image` with `--new-bundle-
format` for v2 and v3 signatures" — confirms v3.0.5 still accepted the
  flag.
- cosign v3.0.6 release notes (`https://github.com/sigstore/cosign/
releases/tag/v3.0.6`) + PR #4762 (`Disallow --new-bundle-format and
--rfc3161-timestamp`): the v3.0.6 flag-cleanup that finalizes the
  migration.
- cosign v2.6.0 release notes (`https://github.com/sigstore/cosign/
releases/tag/v2.6.0`): canonical example of the v3-default sign-blob /
  verify-blob args shape: `cosign sign-blob --use-signing-config --bundle
sigstore.json README.md`.
- Slice 080's decisions log: `docs/audit-log/080-fix-release-tag-
infrastructure-decisions.md` — full iteration cascade documentation.
- GoReleaser v2 `signs:` schema: `signature:` and `certificate:` are
  the two filename templates the release-asset-discovery logic
  understands; no separate `bundle:` field exists, so repurposing
  `signature:` is the cleanest path.
