---
id: metrics
title: Metrics
routes: [/dashboards/metrics]
terms: [board-kpi, metric-cascade, metric-target]
diagrams: []
---

A board wants a handful of numbers that say whether security is getting
better. The people doing the work need finer numbers they can act on.
Metrics connects the two: each board-level KPI is fed by program
metrics, and those by team metrics, so a number that moves at the top
can be traced to the work underneath it.

## How metrics work

Each metric has a value over time. A metric's values are either
computed by the platform from your own records, such as how many
controls pass or how fresh your evidence is, entered by hand, or
brought in from another tool.

A metric can have a target, plus warning and critical levels. Its badge
is green when the latest value meets the target, yellow when it falls
short but is above warning, and red when it passes critical. The colour
is worked out from the numbers alone; nothing interprets them for you.

## What this page shows

A card for each board KPI, with its latest value, target and the last
90 days. Select a card to walk its cascade: the program and team
metrics beneath it. Open a metric to see its full history against its
target, its parents and children, and recent values entered by hand.

## Who can do what

- **See metrics**: admins and GRC engineers.
- **Set a metric's target**, and its warning and critical levels:
  admins.
- **Enter a value** for a metric that is entered by hand: admins. Each
  entry is kept with who made it and when.
