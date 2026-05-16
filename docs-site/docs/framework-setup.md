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

The SOC 2 framework version and its STRM crosswalk ship as a JSON
bundle. The bootstrap container loads it automatically; on a clean
re-import:

```sh
just atlas-cli framework-import \
  --bundle controls/frameworks/soc2-v2017.json
```

Confirm the framework version is registered:

```sh
curl -fsS http://localhost:8080/v1/frameworks
# [ { "slug": "soc2", "version": "v2017", "requirement_count": 64, ... } ]
```

## Step 3 — inspect a crosswalk

Pick a SOC 2 requirement and traverse to its SCF anchors + your tenant's
controls:

```sh
curl -fsS http://localhost:8080/v1/requirements/soc2:v2017:CC6.1/coverage | jq
```

A shape you should see:

```json
{
  "requirement": { "framework_version_id": "...", "code": "CC6.1" },
  "anchors": [
    {
      "scf_id": "IAC-06",
      "edge": { "relationship_type": "subset_of", "strength": 8 },
      "controls": [
        { "id": "...", "title": "Identity and access management baseline" }
      ]
    }
  ]
}
```

Read this as: **CC6.1** is a subset of **IAC-06**; your tenant has one
control anchored to IAC-06; therefore CC6.1 is satisfied through that
control. No CC6.1-specific control was ever needed.

## What "no relationship" means

STRM stores "confirmed no overlap" as data. The coverage endpoint
filters those edges out by design — they surface only in the
mapping-inspector UI, not the coverage view.

## Where SOC 2 v2017 came from

The crosswalk JSON committed at `controls/frameworks/soc2-v2017.json`
was machine-generated from the SCF organisation's STRM workbook (SOC 2
Trust Services Criteria v2017). The SCF standard license permits this
redistribution; the file ships with the repo. The bundling decision is
captured in [Plans/canvas/11-open-questions.md](https://github.com/mgoodric/security-atlas/blob/main/Plans/canvas/11-open-questions.md)
under the SCF redistribution item.

## Next steps

- [First audit →](first-audit.md) — run an end-to-end audit cycle from
  AuditPeriod creation through OSCAL SSP export

---

## Was this helpful?

Tell us in [GitHub Discussions](https://github.com/mgoodric/security-atlas/discussions).
