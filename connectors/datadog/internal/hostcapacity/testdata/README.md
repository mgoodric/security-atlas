# hostcapacity testdata: built from the docs, not recorded

These four `/api/v1/query` responses were **built by hand to Datadog's
documented response shape**. They were **not recorded from a live Datadog
tenant**, and nothing in this surface has been checked against one (#2935).

Sources, read 2026-11-22:

- Response shape (`status`, `series[].scope`, `series[].pointlist` as
  `[ms, value]` with null gaps, `interval` in ms):
  <https://docs.datadoghq.com/api/latest/metrics/query-timeseries-points.md>
- Metric names and units (`system.cpu.idle` percent, `system.mem.pct_usable`
  fraction): <https://docs.datadoghq.com/integrations/system/>
- `system.disk.used` / `system.disk.total` bytes, tagged `device`:
  <https://docs.datadoghq.com/integrations/disk/> and the System check's Tags
  section.

`gen_fixtures.py` regenerates them:

```bash
uv run --no-project python connectors/datadog/internal/hostcapacity/testdata/gen_fixtures.py \
  connectors/datadog/internal/hostcapacity/testdata
```

The pre-commit prettier hook then reformats the JSON; the values are
unchanged.

The window is 2026-11-21T11:55:00Z to 12:00:00Z. `web-1` has a full window
(20 CPU points, p95 76, memory 60%, disks 40% and 80%). `web-2` has 6 CPU
points (under `MinCPUSamples`), memory 95%, and a disk with no total.

When a live response is captured, replace these files with it and drop this
note.
