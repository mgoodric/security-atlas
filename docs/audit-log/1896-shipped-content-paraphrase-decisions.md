# #1896 — Paraphrase verbatim standard text in shipped content

**Ticket:** https://github.com/mgoodric/security-atlas/issues/1896 (from the #1888 / #1891 review)
**Date:** 2026-11-13

## Detection-tier classification

- `detection_tier_actual`: `manual_review`
- `detection_tier_target`: `manual_review`

Whether a sentence is too close to a copyrighted source is a reading call.
No automated tier makes it.

## D1 — Same posture as #1888 and slice 438

Every change keeps the identifier and replaces the prose with a short
original summary of what the requirement asks for (#1888 D1, slice 438
D5). Ids, COSO principle numbers, titles of crosswalk rows, anchors and
every mapping row are unchanged. Each summary was written from the
requirement's intent, not by rewording the source sentence: no "The entity
..." openers, no "shall"-to-indicative swaps, and a different sentence
structure from the source where the source was a single list of verbs.

**Confidence: MEDIUM.** The project has used this posture since slice 438;
none of the standards has had legal review, which stays with the open SCF
redistribution item.

## D2 — What changed

| File                                     | Change                                                                                                                                                                                                                         |
| ---------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `internal/api/anchorseed/data.go`        | 5 SOC 2 and 5 ISO 27001 sample `Text` values (both had the source sentence, ISO with "shall"). NIST CSF texts stay: public domain.                                                                                             |
| `data/crosswalks/iso27001-2022.yaml`     | All 93 bodies. Nearly all were the Annex A control sentence with "shall" swapped for the indicative, which the header said the file did not do. Titles stay: the header treats the Annex A short titles as factual references. |
| `data/crosswalks/pci-dss-4.0.yaml`       | All 31 bodies. Most tracked the requirement sentence. Titles stay (see D4).                                                                                                                                                    |
| `data/crosswalks/soc2-tsc-2017.yaml`     | PI1.1 body, the closest to the source phrasing after #1891 (from the #1891 review).                                                                                                                                            |
| `deploy/docker/docker-compose.proxy.yml` | `atlas-bootstrap` sets `ATLAS_COMPOSE_ARGS` to both `-f` files, so the printed opt-in command targets the proxy stack (from the #1891 review). `test-self-host-bundle.sh` expects the overlay in proxy mode.                   |

## D3 — What the next boot updates

- **SOC 2 crosswalk:** the bootstrap imports it on every boot.
  `soc2import.Import` sees PI1.1's body differ and updates that one
  requirement row. Edge re-seeding (#1873, ADR 0018 §2) keys on
  `edgeContentEqual`, which ignores requirement text, and no mapping row
  changed, so no edge or tier re-seeds.
- **ISO 27001 and PCI DSS:** opt-in, not imported by the bootstrap. An
  install that imported them keeps the old bodies until the operator
  re-runs the printed import command; that run updates 93 / 31 requirement
  rows and, with no mapping change, re-seeds no edge or tier.
- **anchorseed:** compiled in; the next server build serves the new text.

## D4 — Stock control descriptions deferred to #1909

The first push of this PR also reworded 45 `controls/soc2/*/control.yaml`
descriptions and added a licensing note to `controls/soc2/README.md`
(commit `f2fbdcd1`). Review blocked it on the upgrade path, not the
wording. Phase 5b re-uploads every bundle on every boot, and
`control.Store.Upload` (`internal/control/store.go`) supersedes a control
whenever the manifest hash changes, carrying nothing forward. On an
upgraded install those 45 controls would have lost their owners, evidence
and freshness, evaluations, exceptions, and their risk, decision,
action-plan, change and incident links. Those edits are reverted here.
#1908 adds in-place, description-only updates; #1909 re-applies the
descriptions from `f2fbdcd1` once #1908 lands.

## D5 — Left as is

- **PCI titles.** Several titles are close to the PCI DSS section headings
  (e.g. 6.2.1 "Bespoke and custom software is developed securely"). The
  header treats short titles as factual references, the same call slice
  438 made for ISO titles. Changing titles is a posture change, not a
  wording fix, so it is not made here.
- **`ucfcoverage` contract fixture.** `internal/api/ucfcoverage/handler_contract_test.go`
  and `web/lib/contracts/control-coverage.golden.json` use the fragment
  "The entity implements logical access security measures." It is test
  data, not in the bootstrap image, and the golden is generated from the
  Go test, so it is left alone.
- **NIST CSF and HIPAA.** Public domain; out of scope per the ticket.
