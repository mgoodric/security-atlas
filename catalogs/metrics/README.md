# Metrics catalog

This directory holds the platform's curated metrics catalog: the
opinionated set of measurements security-atlas computes (or accepts
manual entry for) on behalf of every tenant. Slice 076 lands the
backbone; future slices may extend the catalog or add per-tenant
customization.

## Authoring rules

- **One YAML file per board-level cascade**. The board metric and every
  descendant program / team metric in its cascade live in the same file
  so a reader sees the whole rollup as one document.
- **Every metric answers a question a v1 persona actively asks** — the
  solo security leader at a 50-150-person security-product startup.
  Vanity metrics ("might be useful someday") are out of scope. The
  decisions log records what was rejected and why.
- **`compute_strategy` is one of**:
  - `computed` — a Go evaluator in `internal/metrics/eval/` computes
    the value on the 15-minute cron. `compute_evaluator` MUST name the
    registered evaluator. The DB CHECK enforces the iff.
  - `manual_input` — the user submits values via
    `POST /v1/metrics/{id}/inputs`. The trigger replicates each input
    to `metric_observations` so the read series is unified.
  - `external_integration` — placeholder for a future per-metric
    connector slice (slice-044/045/046 shape). v1 treats these like
    `manual_input` (UI prompts the user); when the connector lands the
    metric flips to `computed`.
- **`level` is one of** `board` / `program` / `team`. The cascade
  proceeds board → program → team; arbitrary cross-level edges are
  permitted but the seeder's cycle detector rejects loops.
- **`cadence` is one of** `realtime` / `daily` / `weekly` / `monthly` /
  `quarterly`. The 15-min cron runs every computed metric regardless of
  cadence; the cadence string is a hint to the UI and to the maintainer
  about how often the value meaningfully changes.
- **`source_slices`** lists the slice numbers a `computed` metric reads
  from. Used by the docs reference generator to surface the data lineage.
- **`notes`** is the maintainer's narrative — what this metric is for,
  what NOT to measure alongside it, what to revisit. Treat it like
  prose, not a schema description.

## Cascade discipline

A child metric carries one or more `parents:` entries that name parent
metric ids (text slugs). Every parent reference is resolved at boot;
unresolved references abort the seeder. Cycle detection is an app-layer
topological sort at seed time (the migration's DB-level CHECK only
forbids the trivial self-loop case).

## Adding a metric

1. Add it to the appropriate `<board-metric>.yaml` file (or create a
   new board file if the metric is a new board-level cascade root).
2. If the metric is `computed`, register the evaluator in
   `internal/metrics/eval/registry.go`.
3. Re-run the seeder (it runs automatically at boot; the
   `just seed-metrics` recipe re-seeds on demand).
4. Re-generate the docs reference: `just metrics-reference`.
