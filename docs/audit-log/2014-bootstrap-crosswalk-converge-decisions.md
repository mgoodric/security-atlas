# Issue #2014 — Boot converges every imported bundled crosswalk, not only SOC 2

The bootstrap re-imported only `soc2-tsc-2017.yaml` on boot. After the SCF
2026.3 remap groups, upgraded installs kept HIPAA, ISO 27001, PCI DSS and NIST
CSF edges on retired anchors until an operator re-ran `import-crosswalk` by
hand.

- `detection_tier_actual`: manual_review
- `detection_tier_target`: integration

## D1 — Re-import, and log what retires (Matt's decision, 2026-11-16)

**Decision.** On every boot, re-import each bundled crosswalk whose framework
this install already imported, through the existing import path. Print every
retired edge. Skip frameworks the install never imported, and never let a
warning or a per-file failure block boot.

**Why.** The ticket offered two options: re-import, or print a "crosswalk out
of date" warning. A warning leaves the stale anchors in place, still counting
as coverage, until someone acts on it. Re-importing is what a manual
`import-crosswalk` already does, and it is what boot already did for SOC 2.

**What operators see.** The import retires an edge the bundled file no longer
lists when the bundled file's source owns it (`ownsEdge`). That covers two
kinds of hand-made edge, and Matt accepted both:

1. Edges written before #1971, which carry no `crosswalk_source`. They include
   the stale bundled anchors this change exists to move, and any custom YAML an
   operator imported before #1971.
2. Edges from a bundled file the operator edited in place. They carry the same
   `yaml:<basename>` source as the shipped file.

`docs-site/docs/upgrade.md` documents both cases and the remedy: import a
custom crosswalk under its own filename. A retired edge keeps its row and its
tier history, and re-importing the operator's file restores it.

The re-import never touches these, and the integration test asserts the first
two:

- edges copied by an approved SCF remap (`scf-remap:<id>`);
- tenant overlay edges (`fw_to_scf_overlay_edges`), which the importer never
  reads;
- edges from a differently named YAML;
- workbook imports.

The alternative was to skip any framework that still has NULL-source edges
and print a warning. That leaves pre-#1971 installs unconverged, which is the
bug, so it was rejected.

## D2 — The logic lives in Go, called from the shell

**Decision.** `soc2import.ConvergeImported(ctx, pool, dir, exclude...)` holds
the logic, and `atlas-cli catalog converge-crosswalks <dir>` exposes it.
`bootstrap.sh` calls the command with `--exclude soc2-tsc-2017.yaml`, after its
existing SOC 2 import.

**Why.** Logic written in shell could only be tested through the docker
bundle test. A Go function gets a real-Postgres integration test next to the
other crosswalk upgrade tests. SOC 2 keeps its own mandatory, fail-closed
import step, so a SOC 2 failure still stops boot the way it did before.

## D3 — "Already imported" means a platform framework version exists

**Decision.** A file is converged when `ListFrameworkVersionsBySlug` returns
at least one platform version for its `framework_slug`.

**Why.** This is the same lookup the catalog already uses, and it does not
depend on the bundled `framework_version`. If a later bundle ships a new
version of an imported framework, boot imports that new version, which is the
upgrade the operator opted into by importing the framework.

## D4 — Failures are warnings

**Decision.** A load or import failure is recorded on that file's result, and
the remaining files still run. The command prints `warning: converge <file>:
<err>` and exits non-zero. `bootstrap.sh` logs a WARNING and continues.
Import warnings (frozen "requirement kept", missing `scf_release`, legacy
points of focus) go to stderr and do not change the exit status.

## D5 — Log shape

Each framework gets one line: `converge framework=<slug>:<version> file=<f>
edges created= updated= unchanged= retired= restored= kept= requirements
kept=`. Each retired edge gets one line: `converge framework=<slug> edge
retired <code>-><anchor>: ...`. A framework that is not imported gets a
`skipped` line. `kept` counts unlisted edges that another source wrote.
`requirements kept` counts frozen requirements that kept their stored text.
