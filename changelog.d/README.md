# Changelog fragments

Unreleased changes live here, one file per PR, instead of at the top of
`CHANGELOG.md`. Parallel PRs each add their own file, so none of them
conflict on the changelog. At release time the fragments are assembled
into `CHANGELOG.md` and deleted (#2357).

## Adding one

Name the file `<name>.<type>.md`:

- `<name>` is the PR or issue number (`2357`). For a second entry from
  the same PR, use `2357-2`. Any lowercase `[a-z0-9-]` name is accepted.
- `<type>` picks the section: `added`, `changed`, `deprecated`,
  `removed`, `fixed`, `security`, `performance`, `documentation` or
  `quality`.

The body is one Markdown bullet, written the way entries have always been
written here:

```markdown
- **fix(calendar): `/calendar` no longer crashes when an offboarding checklist is due (#2215).** What changed, for whom, and any upgrade note.
```

## Which PRs need one

CI (`changelog-fragment`) fails a PR that changes code and adds no
fragment. Only a fragment the PR adds or modifies directly in
`changelog.d/`, with a valid name, counts. Deleting one, or adding a file
in a subdirectory, does not. It is exempt when the PR:

- carries the `no-changelog` label (nothing user-visible: tests, CI,
  refactors). Add the label, then re-run the failed job;
- changes only docs (`*.md`, `docs/`, `docs-site/`, `Plans/`). Scripts
  under `isa/` (`check-probes.sh`, `run-probes.sh`, `isa/probes/`) are
  code, so they need a fragment or the label;
- is opened by Dependabot, or is a release-please release PR.

## Commands

```bash
bash scripts/changelog.sh check            # validate every fragment
bash scripts/changelog.sh render           # preview the assembled sections
bash scripts/changelog.sh assemble 1.22.0  # release time: write them under
                                           # "## [1.22.0]" and delete them
```

`assemble` runs `check` first. If any fragment is invalid it exits
non-zero before it touches `CHANGELOG.md` or deletes anything. On success
it deletes exactly the fragments it rendered.

## Release-please hand-off

release-please regenerates its release PR branch on every push to
`main`, and that overwrites an `assemble` commit made on the branch. So:

1. Wait until everything meant for the release is merged to `main`.
2. Immediately before merging the release PR, check out its branch, run
   `bash scripts/changelog.sh assemble <version>` (the version in its
   `## [<version>]` heading), and push the commit to that PR.
3. If anything merges to `main` before the release PR does,
   release-please rewrites the branch and the assembled entries are gone
   from it, while the fragments are still on `main`. Run step 2 again.
4. Merge the release PR right after its checks pass.

This is a manual step for now. Automating it would need the workflow to
push to the release PR branch, a new write permission (#2366 decisions
log).
