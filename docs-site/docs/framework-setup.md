# Framework setup — SCF + SOC 2

<!-- Slice 057 shipped the control-detail screenshot at
     docs/images/control-detail.png. Kept out of the docs-site to
     avoid duplicating ~250 KB; the canonical render is in the README
     "Screenshots" section. -->

<!-- prettier-ignore-start -->
!!! info "What you'll learn"

    - Why every framework maps through SCF anchors
    - How the SOC 2 v2017 crosswalk is loaded
    - How to read a `requirement → anchor → controls` traversal
<!-- prettier-ignore-end -->

## Why SCF is the spine

security-atlas does **not** duplicate controls per framework. Instead,
every framework's requirements map to **SCF anchors** via NIST IR 8477
STRM-typed edges. One control authored against an SCF anchor satisfies
every framework requirement whose mapping points at the same anchor.

This is constitutional invariant #1: **one control, N framework
satisfactions** — never `requirement → requirement` directly.

| Concept                   | What it is                                                                            |
| ------------------------- | ------------------------------------------------------------------------------------- |
| **SCF anchor**            | A spine control identifier (~1,400 in the SCF catalog) — the canonical control unit   |
| **STRM edge**             | A typed mapping between a framework requirement and an SCF anchor (e.g., `subset_of`) |
| **Framework requirement** | A row in a `framework_version` (e.g., SOC 2 v2017 `CC6.1`)                            |
| **Control**               | A tenant-authored implementation anchored to an SCF anchor                            |

## Step 1 — the SCF catalog is already seeded

The `atlas-bootstrap` container imported the SCF catalog on first boot
(see [Install](install.md#whats-seeded-for-you)). You can confirm:

```sh
curl -fsS http://localhost:8080/v1/anchors | jq '.items[0]'
```

You should see a JSON anchor row with `scf_id`, `domain`, and
`description`. If the response is empty, re-run bootstrap:

```sh
docker compose -f deploy/docker/docker-compose.yml \
  run --rm atlas-bootstrap
```

## Step 2 — load SOC 2 v2017

The SOC 2 framework version and its STRM crosswalk ship as a YAML
crosswalk at `data/crosswalks/soc2-tsc-2017.yaml`. The bootstrap
container loads it automatically; on a clean re-import, point
`DATABASE_URL` at the `atlas_migrate` role and run:

```sh
just import-soc2 data/crosswalks/soc2-tsc-2017.yaml

# equivalently, without `just`:
security-atlas-cli catalog import-crosswalk data/crosswalks/soc2-tsc-2017.yaml
```

The import is idempotent — re-running the same file is a no-op.

Confirm the framework is registered:

```sh
curl -fsS http://localhost:8080/v1/frameworks
# { "frameworks": [ { "slug": "soc2", "name": "SOC 2 (System and Organization Controls 2)", ... } ] }
```

The import registers framework version `2017` with 43 requirements: the
Common Criteria (CC1.1-CC9.2) plus the Availability, Confidentiality and
Processing Integrity criteria. A requirement's natural key is
`soc2:2017:<code>`, e.g. `soc2:2017:CC6.1`.

## Step 3 — inspect a crosswalk

Pick a SOC 2 requirement and traverse to its SCF anchors + your tenant's
controls:

```sh
curl -fsS http://localhost:8080/v1/requirements/soc2:2017:CC6.1/coverage | jq
```

A shape you should see:

```json
{
  "requirement": { "framework_version_id": "...", "code": "CC6.1" },
  "anchors": [
    {
      "scf_id": "IAC-37",
      "edge": { "relationship_type": "subset_of", "strength": 8 },
      "controls": [
        { "id": "...", "title": "Identity and access management baseline" }
      ]
    }
  ]
}
```

Read this as: **CC6.1** is a subset of **IAC-37**; your tenant has one
control anchored to IAC-37; therefore CC6.1 is satisfied through that
control. No CC6.1-specific control was ever needed.

## What "no relationship" means

STRM stores "confirmed no overlap" as data. The coverage endpoint
filters those edges out by design — they surface only in the
mapping-inspector UI, not the coverage view.

## Where SOC 2 v2017 came from

The crosswalk at `data/crosswalks/soc2-tsc-2017.yaml` is a
`community_draft`: each SOC 2 → SCF edge is a project-authored mapping,
not SCF's published crosswalk. The AICPA Trust Services Criteria and the
COSO principles behind CC1-CC5 are copyrighted, so the file carries only
the criterion identifiers, short project-authored titles, and original
one-line summaries. It reproduces no AICPA or COSO wording; the
`LICENSING POSTURE` header in the file says so. Read the criteria
themselves from the AICPA publication.

The SCF anchors the edges point at come from the separately imported SCF
catalog. The SCF bundling decision is captured in
[Plans/canvas/11-open-questions.md](https://github.com/mgoodric/security-atlas/blob/main/Plans/canvas/11-open-questions.md)
under the SCF redistribution item.

### Upgrades re-seed changed SOC 2 edges

The bootstrap imports this file on every boot. When an image upgrade
changes a SOC 2 mapping row (its relationship type, strength, source
attribution or rationale), the next boot updates that edge and re-seeds
its review tier to the tier its source starts at, with an audit row
(ADR 0018 §2). A reviewer's verified tier on a changed edge does not
survive the upgrade; re-review it. A change to a requirement's title or
summary alone updates the requirement and leaves edge tiers alone.

## Next steps

- [First audit →](first-audit.md) — run an end-to-end audit cycle from
  AuditPeriod creation through OSCAL SSP export

---

## Was this helpful?

Tell us in [GitHub Discussions](https://github.com/mgoodric/security-atlas/discussions).
