# #1982 — SCF 2026.3 remap, THR family group: decisions log

detection_tier_actual: unit
detection_tier_target: unit

The twelfth and last #1717 family group. It uses the same mechanics as #1981 (`1981-scf-pri-sea-tda-vpm-remap-decisions.md`). The sample stays on the open working label `test-2026.3`. The remap rows go into `data/scfremap/remaps.json`. The boot-time re-bind and the crosswalk edge retirement from #1999 do the upgrade work. No migration, and no boot, re-bind or remap code was touched. The one failure seen was expected:

- `TestSampleFixtureContentIsPinnedToItsRelease` flagged the changed anchor content, so the open label's hash was updated.

`TestStockManifestHashes_CoverControlsDir` stayed green: no stock control cites a THR code, so `scripts/gen-stock-control-hashes.sh` had nothing to append.

## D1 — The remap

| Sample ID | 2026.3 ID | 2026.3 title (verbatim)                | Basis in `remaps.json` |
| --------- | --------- | -------------------------------------- | ---------------------- |
| `THR-01`  | `THR-02`  | Threat Intelligence Program            | legacy                 |
| `THR-02`  | `THR-11`  | Indicators of Exposure (IOE)           | legacy                 |
| `THR-03`  | `THR-04`  | Threat Intelligence Feeds              | legacy                 |
| `THR-04`  | `THR-13`  | Threat Hunting                         | concept                |
| `THR-05`  | `THR-15`  | Insider Threat Program                 | concept                |
| `THR-06`  | `THR-16`  | Insider Threat Awareness               | concept                |
| `THR-07`  | `THR-12`  | Vulnerability Disclosure Program (VDP) | concept                |
| `THR-09`  | `THR-06`  | Threat Catalog                         | legacy                 |
| `THR-10`  | `THR-08`  | Threat Analysis                        | legacy                 |

All rows come from D5 in `1717-scf-pin-ratchet-decisions.md`, and none is a low-confidence pick there. Every sample title was already the 2026.3 title of its target, so only the codes move; titles are verbatim from `internal/api/scfseed/testdata/scf-pin.json`. Descriptions stay in our own words and are unchanged. The THR rows were re-sorted by their new code inside the THR block. No row changes family, so this group adds nothing to #2405.

## D2 — One substitution, three chains

Three targets are also sources: `THR-01`→`THR-02`→`THR-11`, `THR-03`→`THR-04`→`THR-13`, and `THR-09`→`THR-06`→`THR-16`. All nine moves were one Perl `s///g` pass that maps each match through a lookup table, never one `sed` per ID:

```
my %m = ('THR-01'=>'THR-02','THR-02'=>'THR-11','THR-03'=>'THR-04','THR-04'=>'THR-13','THR-05'=>'THR-15','THR-06'=>'THR-16','THR-07'=>'THR-12','THR-09'=>'THR-06','THR-10'=>'THR-08');
s/(?<![A-Za-z0-9.-])(THR-(?:0[1-79]|10))(?![0-9]|\.[0-9])/$m{$1}/g;
```

Each match is replaced once and never re-read, so nothing moves twice. The lookahead leaves dotted children alone; none is cited outside `scf-pin.json`.

**Each reference landed exactly once.** A check script compared each of the 18 substituted files against `HEAD`. It extracted every `THR-NN` token from the old file, applied the table, and required the result to equal the token sequence in the new file, in order: `18/18 files (135 tokens): every old token landed exactly once`.

**Target codes checked before the change:** `THR-08`, `THR-11`, `THR-12`, `THR-13`, `THR-15` and `THR-16` appeared nowhere outside `scf-pin.json` and the history dirs. `THR-02`, `THR-04` and `THR-06` were sources themselves, handled by the simultaneous pass.

**Out of the pass:** by convention, the existing CHANGELOG entries and `changelog.d/` fragments, `docs/audit-log/`, `docs/issues/`, `Plans/_archive/` and `scf-pin.json`; and `internal/api/scfseed/scfpin_test.go`, edited by hand because its `knownMismatch` keys are the old codes. No stock `control.yaml`, stock policy, questionnaire, OSCAL, web e2e or `isa/probes` file cites a THR code.

**Hand edits after the pass:**

- compound comment forms the bounded pattern cannot see: `csf_loader_test.go` listed `THR-01/03/07/09/10` (now `THR-02/04/06/08/12`), and `thr_finer_crosswalk_integration_test.go` named the range `THR-02..THR-10` (now "every sample THR anchor but THR-02", since the new codes are not a range);
- `thr_domain_integration_test.go` gained a note that slices 635 and 641 seeded the same concepts under the pre-2026.3 codes;
- crosswalk rationales: titles did not change, so no quoted title needed rewriting.

## D3 — The THR anchor seeds (slices 635, 641, 651)

The issue asked for these to be re-checked. Each seed's proof now runs on the new code with the same concept:

- slice 635 (`thr_anchor_integration_test.go`): the SIEM-rule detection anchor is now `THR-02` Threat Intelligence Program, with its CC7.2/CC7.3 and A.5.7/A.8.16 edges; A.5.7 → `THR-02` stays `equal`.
- slice 641 (`thr_domain_integration_test.go`): all nine sample THR anchors resolve under their new codes.
- slices 646 and 651 (`thr_finer_crosswalk_integration_test.go`, `thr_vdp_crosswalk_integration_test.go`, `csf_loader_test.go`): every finer edge moved with its anchor (ID.RA-02 → `THR-04`, ID.RA-03 → `THR-06` and `THR-08`, ID.RA-08 → `THR-12`, CC7.2 and A.8.16 → `THR-13`, A.8.8 → `THR-11`). The honest-gap guard now asserts that `THR-15` and `THR-16` (insider threat) carry no edge. The CSF extra-edge count stays 5.

## D4 — Connector and kind defaults keep their concept

| Default                               | Old                | New                | Concept kept                         |
| ------------------------------------- | ------------------ | ------------------ | ------------------------------------ |
| Datadog `--siem-control`              | `scf:THR-01`       | `scf:THR-02`       | threat intelligence (detection)      |
| `datadog.siem_rule` default anchors   | `MON-02`, `THR-01` | `MON-02`, `THR-02` | monitoring, threat intelligence      |
| `datadog.siem_signal` default anchors | `THR-01`, `IRO-16` | `THR-02`, `IRO-16` | threat intelligence, incident report |

The permutation and the meaning agree on every row (a legacy move, same title), so nothing was re-chosen. No weak fit was found. An explicit old flag value `scf:THR-01` now names Threat Management Policy in 2026.3; the fragment says to change it.

## D5 — Upgrade effect

No stock control or stock policy cites a THR code, so nothing is superseded.

**`THR-02`, `THR-04` and `THR-06` are re-used with another meaning.** In `test-2026.1` they are Indicators of Exposure, Threat Hunting and Insider Threat Awareness; in `test-2026.3` they are Threat Intelligence Program, Threat Intelligence Feeds and Threat Catalog. `RebindControls` moves only same code and same title, so a tenant control on one of the old codes is not re-bound onto the new holder. It is flagged, with the `remaps.json` note naming its new code. A tenant control on any of the other six old codes is flagged the same way.

## D6 — The ratchet is drained and locked at 0

All nine remaining `knownMismatch` entries were THR, so the map is now empty and `knownMismatchCeiling` drops from 9 to 0. Both are kept rather than removed. The guard's stale-entry and ceiling checks cost nothing when empty, and keeping the map with a ceiling of 0 states the rule in code: a future sample row that does not match the pin fails `TestSCFSampleMatchesPinnedRelease` and is fixed in the sample, because there is no room left to allowlist it. Removing the map would make that rule implicit and invite re-adding an allowlist on the next release bump. The comment on the ceiling now says it is locked at 0.

The `test-2026.3` fixture hash is updated one last time. With the remap complete, the label is ready to be cut as a release, at which point `fixtureContentByRelease` freezes its entry (per the comment in `fixturepin_test.go`). Cutting it is not part of this PR.
