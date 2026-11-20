# #2634 — PCI DSS 9.4.1 re-anchored from DCH-27 to DCH-14: decisions log

From the review of #2620 (#2387). PCI DSS 9.4.1 ("all media with cardholder data is physically secured") was anchored on SCF `DCH-27` Media & Data Retention, `intersects_with`, 0.7. The only overlap was the word "Media".

detection_tier_actual: manual_review
detection_tier_target: manual_review

No code bug. Fit is a judgement about meaning, and the #2620 reviewer caught it. The one expected test failure was `TestSampleFixtureContentIsPinnedToItsRelease`, which flagged the new sample row, so the open `test-2026.3` label's hash was re-pinned (as in #2413).

## D1 — Evidence: SCF's own PCI DSS 4.0.1 column

Source: the pinned SCF 2026.3 workbook (sha256 `5a89bf2d3c106a9a87d4b6e3d62dd3e147d0e960d4c07473045a10aa8a7df697`, see `1717-scf-pin-ratchet-decisions.md` D1). The `PCI DSS 4.0.1` column, and every SAQ column, lists 9.4.1 against exactly four controls:

| ID       | 2026.3 title                                                      | Legacy # |
| -------- | ----------------------------------------------------------------- | -------- |
| `DCH-02` | Data Protection                                                   | DCH-01   |
| `DCH-06` | Data Stewardship                                                  | DCH-01.1 |
| `DCH-13` | Media Storage                                                     | DCH-06   |
| `DCH-14` | Physically Secure All Media Containing Sensitive / Regulated Data | DCH-06.1 |

`DCH-27` is not among them.

## D2 — Anchor `DCH-14`, `equal`, 0.9 (Matt, 2026-11-19)

`DCH-14`'s title restates 9.4.1 with "cardholder data" generalized to "sensitive / regulated data". Since a PCI scope carries only cardholder data, the fit is treated as `equal`.

Options weighed:

- **(a) `DCH-14`, `equal`, 0.9** (chosen; Matt, 2026-11-19, relayed by the dispatcher);
- (b) `DCH-14`, `subset_of`, 0.8: hedges that DCH-14 covers all sensitive data, not only CHD;
- (c) `DCH-13` Media Storage, `subset_of`, 0.7: the broader parent;
- (d) keep `DCH-27` at 0.4 LOW with "partial fit" wording: contradicts SCF's own mapping.

## D3 — One sample row

Every crosswalk anchor is a row in `migrations/fixtures/scf-sample.json`, so `DCH-14` is added with its 2026.3 ID and title verbatim and a description in our own words (licensing per 1717 D3). No stock control's `scf_anchor_id` changed, so nothing is superseded on boot. `DCH-27` keeps its other crosswalk edges (ISO, SOC 2, HIPAA) and stays in the sample.
