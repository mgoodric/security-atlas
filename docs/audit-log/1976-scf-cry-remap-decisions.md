# #1976 — SCF 2026.3 remap, CRY family group: decisions log

detection_tier_actual: unit
detection_tier_target: unit

The sixth #1717 family group. It follows #1975 (`1975-scf-cld-cpl-tpm-remap-decisions.md`). The sample stays on the open working label `test-2026.3`. The remap rows go into `data/scfremap/remaps.json`. The boot-time re-bind and crosswalk edge retirement from #1999 do the upgrade work. No migration. The only failure seen was the expected one: `TestSampleFixtureContentIsPinnedToItsRelease` flagged the changed anchor content, so the open label's hash was updated.

## D1 — The remap

| Sample ID | 2026.3 ID | 2026.3 title (verbatim)       | Basis in `remaps.json` |
| --------- | --------- | ----------------------------- | ---------------------- |
| `CRY-01`  | `CRY-02`  | Use of Cryptographic Controls | legacy                 |
| `CRY-04`  | `CRY-07`  | Encrypting Data At Rest       | concept                |
| `CRY-08`  | `CRY-05`  | Encrypting Data In Transit    | concept                |
| `CRY-09`  | `CRY-10`  | Cryptographic Key Management  | legacy                 |
| `CRY-11`  | `CRY-14`  | Cryptographic Hash            | concept (low; D3)      |
| `CRY-12`  | `CRY-06`  | Integrity of Data In Transit  | concept                |

All rows come from D5 in `1717-scf-pin-ratchet-decisions.md`. Titles are verbatim from `internal/api/scfseed/testdata/scf-pin.json`. `scfremap.Parse` accepts only `legacy` and `concept` as a basis, so the low pick is recorded as `concept`, as `AAA-01` and `CLD-09` were.

Descriptions stay in our own words. The two encryption rows gain "through encryption" (D4). The others are unchanged.

## D2 — One substitution, no chain

None of the six target codes (`CRY-02`, `CRY-05`, `CRY-06`, `CRY-07`, `CRY-10`, `CRY-14`) is a source, so no reference can move twice. `CRY-05` was already cited outside the sample, by the walkthrough fixtures, docs, web e2e mocks and the demo seed, all meaning encryption at rest (its pre-2026 meaning). None of those is a sample anchor, so the substitution does not touch them and nothing collides with it. The demo seed is handled in D5. The others are synthetic tenant controls with their own titles, outside this group (#1717's "out of scope" note on non-sample codes).

The six moves were applied as one Perl pass that maps each match through a lookup table. The pattern is bounded, `(?<![A-Za-z0-9.-])(CRY-01|CRY-04|CRY-08|CRY-09|CRY-11|CRY-12)(?![0-9]|\.[0-9])`, so dotted children are untouched. No dotted CRY child was cited anyway.

Left as written:

- existing CHANGELOG entries, `docs/audit-log/`, `docs/issues/`, and `scf-pin.json`;
- `Plans/_archive/mockups/`, which is archived history;
- the questionnaire review-queue e2e spec's question code `CRY-01`. It is a question label, not an SCF anchor (the `TPM-05` precedent in #1975).

Crosswalk rationales and code comments that quoted an old title now quote the 2026.3 title: "Encryption At Rest" becomes "Encrypting Data At Rest", "Encryption In Transit" becomes "Encrypting Data In Transit", "Cryptographic Integrity Verification" becomes "Cryptographic Hash", and "Transmission Integrity" becomes "Integrity of Data In Transit".

## D3 — CRY-11 to CRY-14 (low pick, Matt confirmed)

The sample concept is hash and signature integrity checks. Its only reference is HIPAA 164.312(c)(2), Mechanism to Authenticate ePHI. The candidates sent to Matt were:

- **(a) `CRY-14` Cryptographic Hash** (recommended): names the mechanism, and stays distinct from `CRY-06`, so the HIPAA row keeps its own anchor;
- (b) merge into `CRY-06` Integrity of Data In Transit: loses the at-rest integrity that 164.312(c)(2) covers;
- (c) `CRY-02` Use of Cryptographic Controls: too generic, and collides with `CRY-01`'s target;
- (d) an integrity control outside CRY: overlaps the DCH group's low pick.

Matt chose (a), one to one, on 2026-11-15.

## D4 — Search keeps finding the encryption anchors

Anchor search is a per-token `ILIKE` over `scf_id`, title and description (`internal/api/search/search.go`). The old titles said "Encryption"; the 2026.3 titles say "Encrypting", which `%encryption%` does not match, and the old descriptions never said "encryption". Left alone, a search for "encryption" would stop finding either anchor, and `TestSlice661_AnchorSearch_EmptyTenant` would fail. Both descriptions now end "through encryption". That is still our own wording, and it keeps the search behaviour and both search integration tests as they were.

## D5 — Demo seed: at-rest anchor

`internal/demoseed/writers.go`'s `demoSCFAnchors` draws from the real 2026.3 catalog. A title check found `{"CRY-05", ..., "Encryption of Data at Rest"}`. In 2026.3, `CRY-05` is Encrypting Data In Transit, and Encrypting Data At Rest is `CRY-07`. The row now reads `CRY-07` with the verbatim title. The demo questionnaire's DSI-02 ("Is customer data encrypted at rest?") anchored on `CRY-05` and moves to `CRY-07` with it, so it keeps pointing at the at-rest anchor. The other demo pairings stay with #2302.

## D6 — Upgrade effect

One stock SOC 2 control changes anchor code, so on the next boot it is superseded, not re-bound: `soc2_cc6_7_encryption_in_transit` (`CRY-08`→`CRY-05`). Owners, evidence, exceptions and risk links stay with the old version. Its manifest hash is added by `scripts/gen-stock-control-hashes.sh`. No stock control in this group keeps its code with a changed title, so no title-only re-bind applies here. A tenant control on any of the six old codes in `test-2026.1` is flagged with the note from `remaps.json`.

Connector default anchors move with the substitution: AWS `--control-id`, Azure `--storage-control` and GCP `--bucket-control-id` to `scf:CRY-07`, Azure `--keyvault-control` to `scf:CRY-10`, and k8s `--secret-control` to `scf:CRY-02`. An operator who passed an old code explicitly now names a different 2026.3 control that is not in the bundled sample. The CHANGELOG says so.
