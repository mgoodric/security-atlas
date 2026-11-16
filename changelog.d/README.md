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
fragment. It is exempt when the PR:

- carries the `no-changelog` label (nothing user-visible: tests, CI,
  refactors). Add the label, then re-run the failed job;
- changes only docs (`*.md`, `docs/`, `docs-site/`, `Plans/`, `isa/`);
- is opened by Dependabot, or is a release-please release PR.

## Commands

```bash
bash scripts/changelog.sh check            # validate every fragment
bash scripts/changelog.sh render           # preview the assembled sections
bash scripts/changelog.sh assemble 1.22.0  # release time: write them under
                                           # "## [1.22.0]" and delete them
```

Run `assemble` on the release-please PR branch, after release-please has
written the `## [<version>]` heading, and commit the result to that PR.
