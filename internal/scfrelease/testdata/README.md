# SCF release fixtures (#1770)

Two small SCF catalogs in the `atlas-cli catalog import-scf` JSON shape.
Release B's `legacy_scf_id`s put every release A code in one class of
`scfrelease.Classify`:

| Release A code | In release B                                      | Class        |
| -------------- | ------------------------------------------------- | ------------ |
| IAC-06         | IAC-06, same lineage, same title                  | `unchanged`  |
| GOV-01         | GOV-01, same lineage, new title                   | `renamed`    |
| CRY-03         | CRY-03 and CRY-03.1 both descend from it          | `split`      |
| AST-04         | AST-04 now descends from A's AST-09 (code reused) | `reused`     |
| AST-09         | gone; B's AST-04 descends from it                 | `renumbered` |
| PRM-07         | gone; nothing descends from it                    | `withdrawn`  |

The anchors are not real SCF text. Integration tests load both with
`scfreleasetest.LoadAB`.
