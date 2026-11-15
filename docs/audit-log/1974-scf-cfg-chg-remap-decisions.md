# #1974 — SCF 2026.3 remap, CFG + CHG family group: decisions log

detection_tier_actual: unit
detection_tier_target: unit

The fourth #1717 family group. It follows #1972 and #1973 (`1973-scf-ast-bcd-remap-decisions.md`). The sample stays on the open working label `test-2026.3`. The remap rows go into `data/scfremap/remaps.json`. The boot-time re-bind and crosswalk edge retirement from #1999 do the upgrade work. No migration. The only failure seen was the expected one: `TestSampleFixtureContentIsPinnedToItsRelease` flagged the changed anchor content, so the open label's hash was updated.

## D1 — The remap

| Sample ID | 2026.3 ID | 2026.3 title (verbatim)        | Basis in `remaps.json` |
| --------- | --------- | ------------------------------ | ---------------------- |
| `CFG-02`  | `CFG-04`  | Secure Baseline Configurations | legacy                 |
| `CFG-04`  | `CHG-03`  | Configuration Change Control   | concept                |

Both come from D5 in `1717-scf-pin-ratchet-decisions.md`. Neither is a `low` pick. Both sample titles already matched their 2026.3 titles verbatim, so only the IDs move. `CHG-02` (a `low` pick) is held for Matt's confirmation; see D4.

## D2 — The chain, applied as one substitution

`CFG-02`→`CFG-04` while `CFG-04`→`CHG-03`: the old code `CFG-04` is both a source and a target. A single Perl pass maps each match through a lookup table, so nothing is substituted twice. The pattern is bounded, `(?<![A-Za-z0-9])CFG-0[24](?![0-9]|\.[0-9])`, so `CFG-04.5` Session Termination (moved in from IAC-25 by #1971) is untouched; `git diff | grep CFG-04.5` is empty. Left as written: existing CHANGELOG entries, `docs/audit-log/`, `docs/issues/`, and `scf-pin.json`. No CFG codes appear in the SCF-release diff fixtures, the demo seed, or `rerank_test.go`. Every repo use of `CFG-02` meant Secure Baseline Configurations, and every use of `CFG-04` meant Configuration Change Control (checked line by line), so all of them follow the code.

Hand edits on top:

- **`CHG-03` joins the CHG family.** The row takes `family: CHG` and moves into the sample's CHG block, as #1972 D2 did for AAA→MON. The two stock controls anchored on it take `control_family: CHG`.
- **Prose that quoted an old title.** The PCI DSS 2.2.1 rationale quoted "System Hardening Through Baseline Configurations", a legacy SCF title. It now quotes the 2026.3 title, Secure Baseline Configurations.

## D3 — Not changed: the PCI 2.2.4 edge

PCI 2.2.4 (disabling unnecessary services) sits on Configuration Change Control as its "least-functionality facet". SCF has a closer control (`CFG-03` Least Functionality), but it is not in the sample, and moving the edge changes the mapping, not just the code. The remap keeps the meaning the edge already had.

## D4 — CHG-02 (pending Matt)

The sample's `CHG-02` "Change Control Process" has no same-titled 2026.3 control. D5 picks `CHG-02` Change Management Program with `low` confidence. The candidates sent to Matt are `CHG-01` Change Management Policy, `CHG-02` Change Management Program, and `CHG-03` Configuration Change Control. `CHG-03` is the closest in meaning, but it would merge with the sample `CFG-04` row. The recommendation is the D5 pick.

## D5 — Upgrade effect

Two stock SOC 2 controls change anchor code from `CFG-04` to `CHG-03`, so on the next boot they are superseded, not re-bound: `soc2_cc3_4_change_risk_reassessment` and `soc2_cc8_1_configuration_change_control`. A tenant control on old `CFG-02` or `CFG-04` in `test-2026.1` is flagged and not re-bound. `RebindControls` moves only rows with the same code and the same title, and in `test-2026.3` those codes carry different titles. The control's note names the new code from `remaps.json`. The k8s connector's four `scf:CFG-02` default control flags become `scf:CFG-04`.
