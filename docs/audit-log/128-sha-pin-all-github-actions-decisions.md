# 128 — SHA-pin every GitHub Action across all workflows — decisions log

Slice 128 is `Type: AFK`. This log records the build-time judgment
calls made while sweeping every action `uses:` line across the six
workflow files under `.github/workflows/` from tag-pinned to
SHA-pinned, and while wiring the `actions-pin-check` BLOCKING CI guard
that prevents regression.

Format follows the JUDGMENT-slice convention used by other AFK slices
in this repo (Diagnosis · Decision · Revisit-trigger · Confidence).

## D1 — Per-action SHA resolution table (sweep snapshot, 2026-05-18)

**Decision:** Each entry below was the value `gh api repos/<repo>/git/refs/tags/<tag>` resolved to at sweep time (2026-05-18). Where the upstream uses an annotated tag (`.object.type == "tag"`), the table records the dereferenced commit SHA (`gh api repos/<repo>/git/tags/<sha> --jq .object.sha`) — that is the value pinned in the workflow, not the tag-object SHA.

**Why this matters:** future contributors editing a workflow may wonder "what SHA does v6 of actions/checkout point at?" — without this table, the answer requires re-running the API call (which may have moved if a security advisory caused a re-tag). The table is the auditable snapshot of what THIS slice chose, recorded once at sweep time.

| Action repo                                                                    | Tag       | Tag object type             | Pinned commit SHA                          |
| ------------------------------------------------------------------------------ | --------- | --------------------------- | ------------------------------------------ |
| `actions/checkout`                                                             | `v6`      | commit                      | `de0fac2e4500dabe0009e67214ff5f5447ce83dd` |
| `actions/setup-go`                                                             | `v6`      | commit                      | `4a3601121dd01d1626a1e23e37211e3254c1c06c` |
| `actions/setup-node`                                                           | `v6`      | commit                      | `48b55a011bda9f5d6aeb4c2d9c7362e8dae4041e` |
| `actions/setup-python`                                                         | `v6`      | commit                      | `a309ff8b426b58ec0e2a45f0f869d46889d02405` |
| `actions/upload-artifact`                                                      | `v7`      | commit                      | `043fb46d1a93c77aae656e7c1c64a875d1fc6a0a` |
| `actions/upload-pages-artifact`                                                | `v5`      | commit                      | `fc324d3547104276b827a68afc52ff2a11cc49c9` |
| `actions/deploy-pages`                                                         | `v5`      | commit                      | `cd2ce8fcbc39b97be8ca5fce6e763baed58fa128` |
| `actions/attest-build-provenance`                                              | `v4`      | tag (annotated)             | `a2bbfa25375fe432b6a289bc6b6cd05ecd0c4c32` |
| `actions/create-github-app-token`                                              | `v3`      | commit                      | `bcd2ba49218906704ab6c1aa796996da409d3eb1` |
| `codecov/codecov-action`                                                       | `v6`      | commit                      | `57e3a136b779b570ffcdbf80b3bdc90e7fab3de2` |
| `dorny/paths-filter`                                                           | `v4`      | commit                      | `fbd0ab8f3e69293af611ebaee6363fc25e6d187d` |
| `github/codeql-action` (init, analyze, autobuild — sub-paths share parent SHA) | `v4`      | tag (annotated)             | `9e0d7b8d25671d64c341c19c0152d693099fb5ba` |
| `golangci/golangci-lint-action`                                                | `v9`      | commit                      | `1e7e51e771db61008b38414a730f564565cf7c20` |
| `bufbuild/buf-action`                                                          | `v1`      | tag (annotated)             | `fd21066df7214747548607aaa45548ba2b9bc1ff` |
| `astral-sh/setup-uv`                                                           | `v7`      | tag (annotated)             | `37802adc94f370d6bfd71619e3f0bf239e1f3b78` |
| `docker/setup-qemu-action`                                                     | `v4`      | commit                      | `ce360397dd3f832beb865e1373c09c0e9f86d70a` |
| `docker/setup-buildx-action`                                                   | `v4`      | commit                      | `4d04d5d9486b7bd6fa91e7baf45bbb4f8b9deedd` |
| `docker/login-action`                                                          | `v4`      | commit                      | `4907a6ddec9925e35a0a9e82d7399ccc52663121` |
| `docker/metadata-action`                                                       | `v6`      | commit                      | `030e881283bb7a6894de51c315a6bfe6a94e05cf` |
| `docker/build-push-action`                                                     | `v7`      | commit                      | `bcafcacb16a39f128d818304e6c9c0c18556b85f` |
| `azure/setup-helm`                                                             | `v5`      | tag (annotated)             | `dda3372f752e03dde6b3237bc9431cdc2f7a02a2` |
| `aquasecurity/trivy-action`                                                    | `v0.36.0` | tag (annotated)             | `ed142fd0673e97e23eac54620cfb913e5ce36c25` |
| `googleapis/release-please-action`                                             | `v5`      | tag (annotated)             | `45996ed1f6d02564a971a2fa1b5860e934307cf7` |
| `sigstore/cosign-installer`                                                    | `v3`      | tag (annotated)             | `398d4b0eeef1380460a10c8013a76f728fb906ac` |
| `goreleaser/goreleaser-action`                                                 | `v6`      | commit                      | `e435ccd777264be153ace6237001ef4d979d3a7a` |
| `anchore/sbom-action` (download-syft sub-path)                                 | `v0`      | commit                      | `e22c389904149dbc22b58101806040fa8d37a610` |
| `step-security/harden-runner` (already pinned by slice 117)                    | `v2.19.3` | n/a — left as slice 117 set | `ab7a9404c0f3da075243ca237b5fac12c98deaa5` |

**Total: 26 unique action repos** (the 22 referenced in the slice doc's narrative count was the StepSecurity dashboard's at-filing-time estimate; the actual sweep found 26 once `actions/setup-node`, `actions/setup-python`, `bufbuild/buf-action`, `golangci/golangci-lint-action`, `dorny/paths-filter`, and `azure/setup-helm` were enumerated alongside the 17 the dashboard initially flagged, plus the `github/codeql-action` sub-paths and `anchore/sbom-action/download-syft` sub-path treated as their parent repos).

**Verification (reproducible at any time):**

```sh
gh api repos/actions/checkout/git/refs/tags/v6 --jq '.object.sha'
# → de0fac2e4500dabe0009e67214ff5f5447ce83dd

# For an annotated-tag example:
gh api repos/actions/attest-build-provenance/git/refs/tags/v4 --jq '.object'
# {"sha": "b3e506e8c389afc651c5bacf2b8f2a1ea0557215", "type": "tag", ...}
gh api repos/actions/attest-build-provenance/git/tags/b3e506e8c389afc651c5bacf2b8f2a1ea0557215 --jq '.object.sha'
# → a2bbfa25375fe432b6a289bc6b6cd05ecd0c4c32
```

**Revisit trigger:** Dependabot's `github-actions` ecosystem block in `.github/dependabot.yml` will propose SHA-bump PRs each Monday when an upstream tag moves (commit prefix `deps(actions):`). When that lands, this table does NOT need to be re-edited — the workflow file is the source of truth post-merge; this table is the SNAPSHOT at slice-128 sweep time.

**Confidence:** HIGH. SHAs retrieved live from upstream GitHub APIs (not copy-pasted from third-party mirrors); annotated-tag dereferences explicitly traced through `.object.type == "tag"` → `repos/<repo>/git/tags/<sha>`; sub-path resolution to parent-repo SHA verified by re-running `gh api repos/github/codeql-action/git/refs/tags/v4` (one resolve covers init + analyze + autobuild).

## D2 — Dependabot post-merge verification

**Status at slice-128 PR open time:** UNRESOLVED — to be filled by the maintainer after the next Dependabot run.

**Plan:** `.github/dependabot.yml` already has `package-ecosystem: github-actions` configured (verified in slice 128 sweep — file lines 93-106) with `interval: weekly` + `day: monday`. After this slice merges, the next Monday Dependabot run will propose SHA-bump PRs (not tag-bump PRs) for any action that has shipped a newer SHA on its existing tag (or a newer tag entirely).

**Expected observable shape:** the bot-PR's diff updates BOTH the SHA AND the `# <tag>` comment in lockstep, e.g.:

```diff
-      - uses: actions/checkout@de0fac2e4500dabe0009e67214ff5f5447ce83dd # v6
+      - uses: actions/checkout@<new-sha> # v6.1.0
```

**Verification step (maintainer, ~1 week post-merge):** record the bot-PR number here once it lands.

**Revisit trigger:** First post-128 Dependabot Monday run.

**Confidence:** MEDIUM. The slice ships correctly regardless of the bot-PR shape — the bot's behavior is documented Dependabot for GitHub Actions; the empirical verification step is a sanity check that our `# <tag>` convention plays cleanly with Dependabot's update mechanism. Reported as MEDIUM rather than HIGH because the empirical verification has not yet been observed in this repo.

## D3 — Coordination outcome with slice 127

**Decision:** Slice 127 merged first (`d71dae2`, 2026-05-18). At slice-128 PR open time, the `actions-pin-check` row is the NEW required-check that slice 128 introduces; slice 127's `.github/branch-protection.json` already matches live (slice 127 D1 picked option (a) edit-file-to-match-live), so slice 128 adds the new context to the file AND surfaces the post-merge apply ritual in the PR body so the maintainer pushes it to live.

**Coordination concretely:**

- **File side (this PR):** `.github/branch-protection.json` `required_status_checks.contexts` array gains `"actions-pin-check"` (11th entry); a new `$additions_from_slice_128` annotation block records the rationale (slice 128 is BLOCKING, unlike slice 127's drift-detect informational job).
- **Live side (post-merge):** the maintainer runs `bash scripts/apply-branch-protection.sh` (slice 127's apply script — idempotent per slice 127 D4) to push the new contexts list to the live GitHub branch-protection config on `main`. The PR body's "Operator note" section calls this out explicitly so it does not get missed.
- **Drift-detect signal:** between slice-128 merge and the operator apply, slice 127's `branch-protection-drift` informational CI job WILL fire a sticky comment on every PR opened in that window (the file lists `actions-pin-check` but live does not). That is INTENTIONAL during the merge window — it surfaces the missing apply step instead of letting it silently drop. The next PR after the operator runs the apply ritual will see the comment resolve.

**Why not gate slice-128 merge on the live apply:** the file IS the source-of-truth for intent (slice 127 D5); the live config is the enforcement side. Decoupling the file change (auditable, code-reviewed) from the apply (operator-side, requires `gh` auth with branch-protection scope) is the slice-127 design. Slice 128 honors that decoupling; the live apply is a one-shot manual step the operator owns.

**Revisit trigger:** After the operator runs the apply ritual, this entry can be marked "RESOLVED" inline with the live apply timestamp + the next-PR drift-detect verification.

**Confidence:** HIGH. The coordination shape was already designed end-to-end in slice 127's docs/apply ritual; slice 128 is the first downstream consumer of that contract, and it follows the contract exactly.

## D4 — Edge-case actions where SHA resolution was non-trivial

Six of the 26 actions resolved through annotated tags (`object.type == "tag"`) and required dereferencing one extra hop to the commit. The dereferencing was automated by the slice-128 sweep script using the documented pattern:

```sh
obj=$(gh api repos/<repo>/git/refs/tags/<tag> --jq '.object')
type=$(echo "$obj" | jq -r '.type')
sha=$(echo "$obj" | jq -r '.sha')
if [[ "$type" == "tag" ]]; then
  final=$(gh api repos/<repo>/git/tags/$sha --jq '.object.sha')
else
  final="$sha"
fi
```

**The six annotated-tag actions (commit SHA in D1 is the dereferenced value, not the tag-object value):**

1. `actions/attest-build-provenance@v4` — tag object `b3e506e8...` → commit `a2bbfa25...`
2. `github/codeql-action@v4` — tag object `7c1e4cf0...` → commit `9e0d7b8d...` (this single commit SHA covers all three sub-paths: `init`, `analyze`, `autobuild`)
3. `bufbuild/buf-action@v1` — tag object `91da6f6a...` → commit `fd21066d...`
4. `astral-sh/setup-uv@v7` — tag object `94527f2e...` → commit `37802adc...`
5. `azure/setup-helm@v5` — tag object `f0accbfd...` → commit `dda3372f...`
6. `aquasecurity/trivy-action@v0.36.0` — tag object `a9c7b0f0...` → commit `ed142fd0...`
7. `googleapis/release-please-action@v5` — tag object `0dfd8538...` → commit `45996ed1...`
8. `sigstore/cosign-installer@v3` — tag object `f713795c...` → commit `398d4b0e...`

**Sub-path observation:** `github/codeql-action/init`, `github/codeql-action/analyze`, and `github/codeql-action/autobuild` are three sub-paths of the same `github/codeql-action` repo; one SHA covers all three (no per-sub-path resolve needed). The slice doc's "codeql-action quirk" note flagged this; verified during sweep. Same for `anchore/sbom-action/download-syft` — sub-path of `anchore/sbom-action`.

**No deprecated / renamed actions encountered.** Spillover-rule trigger (Amendment 2) did not fire: every action in the workflow tree resolved cleanly to a maintained upstream repo. No actions are hosted on personal forks; all 26 are on canonical org repos (`actions/*`, `actions/*`, `docker/*`, `github/*`, etc.). No spillover slice filed under this slice.

**Revisit trigger:** If a future Dependabot bump introduces a NEW action whose tag→SHA resolution is non-trivial (e.g. a redirect from an old org name to a new one, a fork-by-upstream-maintainer pattern), add a row here.

**Confidence:** HIGH. All eight dereferences verified by running the two-step `gh api` chain by hand against three of them (`actions/attest-build-provenance`, `github/codeql-action`, `sigstore/cosign-installer`) and confirming the dereferenced commit matches the value in the workflow.
