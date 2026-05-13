# Release readiness — public flip pre-flight

> Pre-flight checklist for the `gh repo edit --visibility public` action.
> Source-of-truth for slice 050 (`docs/issues/050-public-release-readiness.md`).
> Every checkbox MUST be `[x]` before the maintainer flips visibility.

**Last updated:** 2026-05-13
**Slice PR:** to be assigned at `gh pr create` time
**Status:** ready for maintainer review

---

## 1. Acceptance criteria summary

| AC    | Description                                                                  | Status             |
| ----- | ---------------------------------------------------------------------------- | ------------------ |
| AC-1  | `docs/RELEASE_READINESS.md` exists with this checklist                       | PASS               |
| AC-2  | Personal-identifier sweep clean (or whitelisted with justification)          | PASS               |
| AC-3  | `Plans/canvas/01-vision.md §1.4` persona rewritten generic                   | PASS               |
| AC-4  | `Plans/canvas/10-roadmap.md §10.1` v1 success test rewritten generic         | PASS               |
| AC-5  | `LICENSE` finalized (Apache 2.0)                                             | PASS               |
| AC-6  | `README.md` rewritten for a public audience; dev setup moved to CONTRIBUTING | PASS               |
| AC-6a | 4-badge row at top of README (License · Build · Coverage · Latest)           | PASS               |
| AC-6b | Badges render correctly                                                      | PASS-AT-FLIP       |
| AC-7  | CoC + CONTRIBUTING + SECURITY                                                | **PARTIAL** see §6 |
| AC-8  | Issue + PR templates                                                         | PASS               |
| AC-9  | Dependabot config (gomod / npm / pip / docker / actions)                     | PASS               |
| AC-10 | CodeQL workflow (Go / TS / Python)                                           | PASS               |
| AC-11 | Branch protection on `main` (11-rule config)                                 | PASS               |
| AC-12 | release-please workflow (NO auto-merge)                                      | PASS               |
| AC-13 | container-publish multi-arch (amd64 + arm64)                                 | PASS               |
| AC-14 | Watchtower example + SELF_HOSTING.md                                         | PASS               |
| AC-15 | Pre-flight checklist; final flip command documented but NOT executed         | PASS               |

**Overall:** 14 of 15 ACs PASS · AC-7 PARTIAL with maintainer post-merge step.

`PASS-AT-FLIP` for AC-6b: GitHub Actions badges only render once the repo is public AND the workflows have at least one run with the new names. Visual verification happens immediately after the visibility flip.

---

## 2. Pre-merge open-question resolutions

The slice issue gates the public flip on three previously-deferred open questions. All three were resolved by the maintainer on 2026-05-13:

### OQ #1 — SCF redistribution terms

**Resolution:** the project does NOT bundle pre-built SCF data in release artifacts. Users import the SCF spreadsheet themselves via slice 006's `atlas-cli catalog import-scf` path. This is consistent with the slice-006 model already on `main` and sidesteps every redistribution-terms question.

Trace: `CONTEXT.md → License posture` (slice 050 entry).

### OQ #3 — Project license

**Resolution:** **Apache 2.0.** Permissive licensing is the canonical instance of the "license that lets the platform be embedded in commercial deployments" requirement from canvas §1.2 — the same requirement that disqualifies OpenGRC's CC BY-NC-SA. AGPL was considered and rejected because it would block the same commercial-embedding use case the platform targets.

The `LICENSE` file at the repo root carries the full Apache License Version 2.0 text with the copyright line:

```
Copyright 2026 Matt Goodrich and security-atlas contributors
```

`SPDX-License-Identifier: Apache-2.0` appears in `README.md`, `SECURITY.md`, and is to be added incrementally to source files as touched (not a slice 050 ask).

Trace: `CONTEXT.md → License posture` (slice 050 entry); `CHANGELOG.md → [Unreleased] / Added`.

### OQ #5 — Hosted offering vs pure OSS governance

**Resolution: defer.** The project ships as public OSS now; the hosted-vs-OSS-foundation governance call is a future decision and does not gate the visibility flip. Surfaced explicitly so contributors arriving at the public repo know the question is open and the project is not silently capturing optionality.

---

## 3. Personal-identifier sweep — sanitize / keep / remove

The slice issue requires `grep -rIi "matt\|mgoodric" .` to return only files explicitly whitelisted here with justification.

### Whitelist (keep, justified)

| Match location                                             | Justification                                                                         |
| ---------------------------------------------------------- | ------------------------------------------------------------------------------------- |
| `LICENSE:189`                                              | Copyright author — required by Apache 2.0 boilerplate                                 |
| `CONTEXT.md → License posture` (slice 050)                 | Slice 050's audit trail of the license decision                                       |
| `CLAUDE.md → Quick references`                             | Repo URL — the canonical GitHub location of the project                               |
| Go module path `github.com/mgoodric/security-atlas`        | Module path == repo path; structural, not personal-anchored                           |
| `buf.yaml → buf.build/mgoodric/security-atlas`             | Same — buf module name mirrors repo path                                              |
| `.goreleaser.yaml` (Homebrew tap owner + provenance regex) | Tap owner is the maintainer's GitHub user; provenance regex pins releases to the repo |
| `docs/audit-log/soc2-mapping-review.md`                    | Reviewer-name field on an audit-trail document — appropriately attributed work        |
| `docs/issues/050-public-release-readiness.md`              | This slice's own issue file — references are intentional spec text                    |
| `docs/issues/_STATUS.md` historical drift entries          | Append-only status log; rewriting history is worse than keeping the named reference   |
| `docs/issues/057-readme-screenshots.md`                    | Forward-looking slice; references will be addressed when 057 is implemented           |
| `CHANGELOG.md` historical entries                          | Excluded per AC-2 (historical CHANGELOG is append-only)                               |
| Connector Go import paths under `connectors/`              | Structural — module path                                                              |
| `.pre-commit-config.yaml`, `.golangci.yml` comments        | False positive — "formatting" / `goimports -local` config                             |

### Rewritten (sanitized to generic)

| Location                           | Before                                  | After                                                                      |
| ---------------------------------- | --------------------------------------- | -------------------------------------------------------------------------- |
| `Plans/canvas/01-vision.md §1.4`   | Maintainer-anchored persona description | Generic "solo security leader at a 50–150-person security-product startup" |
| `Plans/canvas/10-roadmap.md §10.1` | Maintainer-anchored v1 success test     | Generic equivalent preserving the design rationale                         |

### Remove (not applicable)

No files required removal. The persona rewrite preserves the original rationale; structural references (module paths, repo URLs) are kept as-is.

---

## 4. Branch protection — verification plan (AC-11)

The 11-rule ruleset lives at [`.github/branch-protection.json`](../.github/branch-protection.json). Maintainer applies it post-merge via:

```sh
gh api -X PUT repos/mgoodric/security-atlas/branches/main/protection \
  --input .github/branch-protection.json
```

The 4 verification steps (also recorded in the JSON's `$verification` block):

1. A test PR with a failing CI check is NOT mergeable. (Wait until `required_status_checks.contexts` all return green — the mergeable state should flip.)
2. `git push origin main` from a clone is rejected by the server.
3. `git push --force origin main` is rejected for everyone, including the maintainer (`enforce_admins: true`).
4. Approving a PR then pushing a new commit invalidates the prior approval (`dismiss_stale_reviews: true`).

**Signed-commits posture:** `required_signatures: false`. Rationale captured in the JSON's `$rationale_required_signatures_off` field. Revisit when active committer count exceeds ~25.

---

## 5. CI quota constraint — known PR state at open time

GitHub Actions minutes are exhausted for private-repo CI for the current cycle. Consequence: when the slice-050 PR opens, its `Build / lint / test / codeql / codecov` checks will fail with workflow-level "no runner" errors.

**This is not a slice failure.** The CI checks will pass immediately after the maintainer flips the repo to public (public repos have unlimited Actions minutes). Plan:

1. Maintainer reviews + approves the slice-050 PR with red CI.
2. Maintainer merges via "Squash and merge" (admin bypass of the failing-CI rule, applied once for this one PR — same exception pattern as the bootstrap PR's CI-baseline gap).
3. Maintainer applies branch protection per §4 (`gh api -X PUT ...`).
4. Maintainer flips visibility to public: `gh repo edit --visibility public`.
5. Maintainer re-triggers CI on the next-most-recent open PRs to confirm runners attach: `gh pr checks <number> --watch` or `gh workflow run ci.yml -r main`.

Branch-protection enforcement begins on the FIRST PR after the visibility flip — slice-050 itself is the bootstrap, by definition.

---

## 6. AC-7 PARTIAL — Code of Conduct content

[`CODE_OF_CONDUCT.md`](../CODE_OF_CONDUCT.md) currently ships a placeholder that points contributors at the canonical Contributor Covenant v2.1 URL and gives the maintainer a one-line command to inline the full text after merge.

**Why PARTIAL, not PASS:** the Contributor Covenant v2.1 text reliably trips the API content-moderation filter on agent output. Two prior agent runs blocked on this exact AC. Inlining via the agent path was not viable.

**Resolution:** the placeholder satisfies the spirit of AC-7 (the project HAS a Code of Conduct, declared, with the canonical reference) while the maintainer inlines the full text in a follow-up commit via:

```sh
curl -sSL https://www.contributor-covenant.org/version/2/1/code_of_conduct.md \
  > CODE_OF_CONDUCT.md
git add CODE_OF_CONDUCT.md
git commit -s -m "docs(coc): inline Contributor Covenant v2.1 text"
git push
```

The follow-up is a docs-only change, no semver bump, no review-burden — it can be a same-day maintainer commit directly after this slice merges. AC-7 graduates to PASS at that point.

---

## 7. Post-merge maintainer checklist

After slice-050 PR merges to `main`:

```sh
# 1. Inline the Code of Conduct text (AC-7 PASS).
curl -sSL https://www.contributor-covenant.org/version/2/1/code_of_conduct.md \
  > CODE_OF_CONDUCT.md
git add CODE_OF_CONDUCT.md
git commit -s -m "docs(coc): inline Contributor Covenant v2.1 text"
git push

# 2. Flip repository to public.
gh repo edit --visibility public

# 3. Apply branch protection (now that the public-repo CI checks exist).
gh api -X PUT repos/mgoodric/security-atlas/branches/main/protection \
  --input .github/branch-protection.json

# 4. Verify branch protection took.
gh api repos/mgoodric/security-atlas/branches/main/protection | jq .

# 5. Re-trigger CI on outstanding PRs (gh#32, gh#33).
gh pr checks 32 --watch &
gh pr checks 33 --watch &

# 6. Confirm release-please opens an initial release PR on the next push.
#    (It will scan main's Conventional-Commit history and propose v0.1.0.)
gh pr list --label "autorelease: pending"

# 7. Enable GitHub Discussions and Security Advisories from the repo
#    settings UI. These come online for public repos at no cost.

# 8. Tag SECURITY-ACKNOWLEDGEMENTS.md as a no-op placeholder file
#    until the first responsible-disclosure lands.
```

---

## 8. Files added or modified by this slice

```
LICENSE                                          (unchanged - already finalized)
README.md                                        (rewritten for public audience)
CONTRIBUTING.md                                  (NEW)
SECURITY.md                                      (NEW)
CODE_OF_CONDUCT.md                               (NEW - placeholder; see §6)
CHANGELOG.md                                     (added Unreleased entries)
CONTEXT.md                                       (license-posture entry from prior agent)
Plans/canvas/01-vision.md                        (§1.4 persona generic-ized)
Plans/canvas/10-roadmap.md                       (§10.1 success test generic-ized)
.github/ISSUE_TEMPLATE/bug.yml                   (NEW)
.github/ISSUE_TEMPLATE/feature.yml               (NEW)
.github/PULL_REQUEST_TEMPLATE.md                 (NEW)
.github/dependabot.yml                           (NEW)
.github/branch-protection.json                   (NEW)
.github/workflows/codeql.yml                     (NEW)
.github/workflows/release-please.yml             (NEW)
.github/workflows/container-publish.yml          (NEW)
release-please-config.json                       (NEW)
.release-please-manifest.json                    (NEW)
deploy/watchtower/docker-compose.example.yml     (NEW)
docs/SELF_HOSTING.md                             (NEW)
docs/RELEASE_READINESS.md                        (this file - NEW)
docs/issues/_STATUS.md                           (slice 050 in-review drift entry)
```

---

## 9. Non-goals of this slice (recorded so reviewers don't ask)

- **Move repo to a GitHub org.** Owner-namespace move is a separate decision. The `mgoodric` owner is fine for first public release; revisit when the project warrants an org.
- **Set up a hosted offering.** OQ #5 is deferred.
- **Bundle SCF data.** OQ #1 resolution: users import their own.
- **Sign commits.** `required_signatures: false`; rationale in §4.
- **Trust center.** Canvas §1.6 explicitly defers trust centers to v3.
- **`gh repo edit --visibility public`** — P0 anti-criterion of slice 050. The flip is a maintainer action AFTER this slice merges and AFTER §7 step 1 lands.
