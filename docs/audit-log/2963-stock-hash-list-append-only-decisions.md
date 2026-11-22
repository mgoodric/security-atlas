# #2963 — the stock hash list is append-only (decisions log)

Found in the #2829 review (PR #2956): `scripts/gen-stock-control-hashes.sh`
dropped cc6_7's `263888e5…`, which #2948 had added.
`internal/api/scfimport/stock_manifests_gen.go` is the list
`IsStockManifest` checks (#2321), so a dropped entry makes a tenant still on
that revision look custom, with no warning.

- detection_tier_actual: manual_review
- detection_tier_target: contract

## D1 — Why the generator dropped it

The generator rebuilt the list from scratch on every run: every
`controls/soc2/*/control.yaml` blob in the history reachable from `HEAD`,
plus the working tree. Nothing carried the existing file forward.

A squash merge keeps only a PR's final tree. When a PR edits a control.yaml
twice and runs the generator between the edits, the intermediate revision's
hash lands in the generated file, but its bytes never appear on `main`. The
next PR that regenerates from `main`'s history recomputes the list and loses
that hash.

The audit found the same thing three times. Each lost hash is a control.yaml
revision that existed only on a PR branch:

| Hash        | Bundle                               | Added by            | Dropped by          | Where the bytes lived                                                                              |
| ----------- | ------------------------------------ | ------------------- | ------------------- | -------------------------------------------------------------------------------------------------- |
| `263888e5…` | `soc2_cc6_7_encryption_in_transit`   | #2948 (`427b0936b`) | #2955 (`c74d6d4aa`) | `a55e7eac3`, #2948's first branch commit; `c5d361acb` (aws:kms:dsse) changed it again before merge |
| `132a42de…` | `soc2_cc6_1_logical_access_baseline` | #2897 (`ea04f7ac3`) | #2913 (`2c64029a5`) | a #2897 branch revision                                                                            |
| `49e33255…` | `soc2_cc4_1_continuous_monitoring`   | #2932 (`bf163410f`) | #2937 (`3fe320438`) | a #2932 branch revision                                                                            |

Checked as follows. Hashing every committed revision of the cc6_7
control.yaml on `main` (`git log` over the path, `git show <c>:<path> |
shasum -a 256`) gives seven hashes, and `263888e5…` is not one of them.
`git show a55e7eac3:…/control.yaml | shasum -a 256` gives `263888e5…`.
Running the old generator on `main` (`861dc8b1b`) reproduces the committed
file exactly (272 hashes), so no merge resolution was involved. The cc6_1 and
cc4_1 rows are inferred from the same mechanism, because the old generator on
`main` omits both. I did not fetch their branch commits.

The same thing dropped `263888e5…` a second time, during #2956's merge of
`main`. #2956 had restored the hash by hand. Merging `main` and then
regenerating rebuilt the list from the merged history, which still did not
hold the bytes, and the hash went again. The worker restored it by hand
before #2956 merged. The self-test reproduces this flow (case 6): two
branches each commit a hash from a draft revision, the PR branch merges
`main` and regenerates, and the old generator keeps 1 of the 3 hashes.

What this means in practice: `main` never shipped any of the three
revisions, so the only tenants they could matter for are installs
bootstrapped from those PR branches. The ticket's "shipped hash" overstates
the reach. The failure mode is still real, and a later regeneration could
drop a revision that `main` did ship in the same way, for example after a
merge resolution took the branch's copy of the file.

## D2 — Union with the existing file, never subtract

The generator now reads the `"<hash>": "<bundle>",` lines already in the
output file and unions them with the history walk, then fails if one hash
maps to two bundle ids, which would otherwise surface as a duplicate-key
compile error. An entry leaves the list only through a hand edit, and the
D3 guard rejects that edit.

Keeping entries that `main` never shipped (D1's three) widens the stock list
by bytes that sat in this public repo's PR branches. That is the same trust
basis as every other entry: the bytes are public, and a tenant cannot add a
hash. I accepted that. The alternative, restricting the walk to `main`,
would drop exactly these entries again.

## D3 — The guard: a superset of the PR base, in `stock-supersede-check`

`scripts/check-stock-hashes-superset.sh <base>` compares the file's entries
with `git show <base>:<file>` and exits 1 when any entry is missing, listing
each as `<hash> <bundle>`. It runs as two steps in the existing
`stock-supersede-check` job. That job is already unconditional and already
in merge-gate's `needs:`, so the guard gates without a new required context.
It runs on `pull_request` only (base = `pull_request.base.sha`), the same
as the supersede step. A merge group carries no labels, so a deliberate drop
(below) would pass its PR run and then fail in the queue. Running in the
queue would catch nothing anyway: the queue builds the group with an
automatic three-way merge, which cannot delete a line that neither side
deleted. A conflict ejects the PR from the queue rather than being resolved
there.

I chose a shell script over a Go unit test because a unit test has no base
branch to read, and CI's shallow checkout has no history.
`scripts/check-stock-hashes-superset_test.sh` is the self-test, with 21
assertions across 10 cases:

- Cases 1-8 (15 assertions) cover the drop itself: an unchanged list, a
  hand-dropped hash, an unresolvable base (exit 2), an added entry, the
  squash-merge loss, the merge-then-regenerate flow (case 6), a merge
  resolved by keeping the branch's copy, and a conflicted file.
- Cases 9-10 (6 assertions) cover the removal path below.

All 21 pass. Swapping in the old generator fails 5: the regeneration count
and the superset check in each of cases 4 and 6, and the conflicted-file
union in case 8. Swapping in the check without the override fails a
different 5, the case 9-10 asserts that use `--labels`.

### Removing a hash

A hash should come off the list only when it is wrong: it is not the sha256
of a `control.yaml` revision that some commit held, or its bundle id is
wrong. Being from a revision `main` never shipped is not enough (D2). The
sanctioned path mirrors `supersedes-stock` (#2891):

1. Delete the line from `stock_manifests_gen.go` by hand. The generator
   never removes an entry.
2. In the same PR, add or change a decisions log under `docs/audit-log/`
   that names each dropped hash in full and says why it is wrong.
3. Add the `drops-stock-hash` label, then re-run `stock-supersede-check`.

With both the label and the logged hash, the check passes, lists each
dropped entry with the log that names it, and raises a `::notice`. Either
one alone still fails. The label is needed because branch protection binds
admins too (`enforce_admins: true`, and the ruleset has no bypass actors),
so overriding the check is not possible, even for the maintainer. The
decisions-log requirement makes a removal leave a reviewable reason on
`main`.

## D4 — Restored entries

The three D1 hashes are back in `stock_manifests_gen.go`. After merging
`main` at #2956, the file has 279 entries: all 276 on `main` plus the 3
restored. I checked that this matches the union of every entry any revision
of the file has held. The audit collects the entries from
`git show <c>:<file>` for each commit in `git log -- <file>`, then diffs
that set against the working tree. Both sides hold 279 entries, and the diff
is empty.
