# #1888 — SOC 2 crosswalk licensing posture and bootstrap opt-in commands

**Ticket:** https://github.com/mgoodric/security-atlas/issues/1888 (from the #1884 review, #1721)
**Crosswalk file:** `data/crosswalks/soc2-tsc-2017.yaml`
**Date:** 2026-11-13

## Detection-tier classification

- `detection_tier_actual`: `manual_review`
- `detection_tier_target`: `manual_review`

The verbatim criterion text and the wrong opt-in commands were found in
review of #1884. Licensing is a reading call no automated tier makes. The
`> 0` floor on assertion 8b was a weak test, not a bug.

## D1 — Paraphrase posture follows slice 438 D5

Since #1884 the published bootstrap image ships `data/crosswalks/` and
imports the SOC 2 file on every boot, so the image redistributes it. The
AICPA 2017 Trust Services Criteria are copyrighted. CC1.1-CC5.3 restate the
seventeen COSO 2013 principles, which COSO also holds copyright on. Several
bodies (CC7.2, A1.2, PI1.x, every CC1-CC5 row) were the criterion
statements word for word, and some titles were long excerpts of them.

The fix applies the posture slice 438 set for ISO (decisions log D5):

| Field   | Before                                   | After                                                     |
| ------- | ---------------------------------------- | --------------------------------------------------------- |
| `code`  | Criterion id                             | Unchanged (a factual reference)                           |
| `title` | Short label, or a long criterion excerpt | Short project-authored label; COSO principle numbers kept |
| `body`  | Criterion statement, often verbatim      | Original one-line summary of the criterion's intent       |

Each summary was written from what the criterion requires, not by
rewording the AICPA sentence, and none opens with the "The entity ..."
form the criteria use. Codes are unchanged, so the 43 requirements and
every mapping row still match. The header now carries a `LICENSING
POSTURE` block.

**Confidence: MEDIUM.** The id-plus-original-summary posture is the one the
project already uses for ISO and PCI; the TSC itself has had no legal
review. That review sits with the open SCF redistribution item.

## D2 — What the change does on the next boot

`soc2import.Import` compares requirement `title` and `body` and, when
either differs, updates the requirement row (`RequirementsUpdated`). Edge
re-seeding (#1873, ADR 0018 §2) keys on `edgeContentEqual`: relationship
type, strength, source attribution and rationale. Requirement text is not
part of it. No mapping row changed here, so an upgraded install updates 43
requirement rows and leaves every edge, and its tier, as it was.

## D3 — Opt-in commands

- **Repo root:** each compose file sets `ATLAS_COMPOSE_ARGS` on its
  bootstrap service. The bundled stack uses `-f deploy/docker/docker-compose.yml`;
  its `.env` sits in the project directory and loads by default. The edge
  stack uses `-p security-atlas-edge -f deploy/docker/docker-compose.edge.yml --env-file deploy/docker/.env.edge`,
  matching the RUN block at the top of that file, since `.env.edge` does
  not load by default.
- **Service name:** `ATLAS_BOOTSTRAP_SERVICE` is `atlas-bootstrap` or
  `atlas-bootstrap-edge`. Both variables have bundled-stack defaults in
  `bootstrap.sh`, so a compose file that omits them still prints a working
  command for the bundled stack.
- **First install only:** the bootstrap reads `frameworks` for `slug = 'soc2'`
  before importing. If no row existed, this boot is the first install and
  the commands print. A marker file on the bootstrap volume was the
  alternative; the database is the record of whether SOC 2 was ever
  imported, and a wiped volume over a kept database should not reprint.

## D4 — Assertion 8b

The floor is now an exact count with two checks:

1. SOC 2 requirements with at least one edge = the number of `- code:`
   rows in the bundled YAML (43).
2. Requirements the stock controls reach through their anchors = that
   same count.

Losing an edge, a requirement, or the stock-control coverage of one
fails the test. All 43 are reachable today: every mapping row's anchor is
anchored by at least one of the 50 stock controls.

## Other bundled crosswalks (checked, not changed)

| File                       | Header              | Finding                                                                                                                                                            |
| -------------------------- | ------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `iso27001-2022.yaml`       | Present (slice 438) | Several bodies sit close to the Annex A control sentences, with "shall" swapped for the indicative (e.g. A.5.10, A.5.11, A.5.13). Worth a pass with the same rule. |
| `pci-dss-4.0.yaml`         | Present             | Bodies are short, but some track the PCI DSS requirement sentences closely (e.g. 2.2.1, 3.5.1). Same pass recommended.                                             |
| `nist-csf-2.0.yaml`        | Present             | Public domain (US government work). No concern.                                                                                                                    |
| `hipaa-security-rule.yaml` | Present             | 45 CFR 164 is public domain. No concern.                                                                                                                           |

## Outside this ticket

The same verbatim TSC sentences appear outside `data/crosswalks/`:
`controls/soc2/*/control.yaml` (most of the 50 stock control descriptions
open with the criterion sentence) and `internal/api/anchorseed/data.go`.
The stock controls are also shipped in the bootstrap image. They need the
same treatment, as a separate ticket.
