"""Builds hostcapacity testdata to Datadog's documented /api/v1/query shape.

Not recorded from a live tenant. Field set from
https://docs.datadoghq.com/api/latest/metrics/query-timeseries-points.md
"""

import json
import sys

START = 1795262100  # 2026-11-21T11:55:00Z
END = START + 300
G = 1e9
PCT = ("percentage", 17, "percent", "percent", "%")
FRAC = ("percentage", 16, "fraction", "fractions", "")
BYTE = ("bytes", 2, "byte", "bytes", "B")


def series(metric, scope, vals, unit):
    return {
        "aggr": "avg",
        "attributes": {},
        "display_name": metric,
        "end": (START + 15 * (len(vals) - 1)) * 1000 + 14999,
        "expression": f"avg:{metric}{{env:production,{scope}}}",
        "interval": 15000,
        "length": len(vals),
        "metric": metric,
        "pointlist": [[(START + 15 * i) * 1000.0, v] for i, v in enumerate(vals)],
        "query_index": 0,
        "scope": scope,
        "start": START * 1000,
        "tag_set": scope.split(","),
        "unit": [
            {
                "family": unit[0],
                "id": unit[1],
                "name": unit[2],
                "plural": unit[3],
                "scale_factor": 1.0,
                "short_name": unit[4],
            }
        ],
    }


def resp(query, group_by, ser):
    return {
        "error": "",
        "from_date": START * 1000,
        "group_by": group_by,
        "message": "",
        "query": query,
        "res_type": "time_series",
        "resp_version": 1,
        "series": ser,
        "status": "ok",
        "times": [],
        "to_date": END * 1000,
        "values": [],
    }


# web-1: 20 CPU points, used = 4, 8, ..., 80; nearest-rank p95 is the 19th, 76.
idle1 = [100 - 4 * (i + 1) for i in range(20)][::-1]
# web-2: 6 non-null CPU points, under MinCPUSamples.
idle2 = [30, None, 25, None, 20, 22, None, 21, 23]

out = {
    "query_cpu_idle.json": resp(
        "avg:system.cpu.idle{env:production} by {host}.rollup(avg, 15)",
        ["host"],
        [
            series("system.cpu.idle", "host:web-1", idle1, PCT),
            series("system.cpu.idle", "host:web-2", idle2, PCT),
        ],
    ),
    "query_mem_pct_usable.json": resp(
        "avg:system.mem.pct_usable{env:production} by {host}.rollup(avg, 15)",
        ["host"],
        [
            series("system.mem.pct_usable", "host:web-1", [0.42, 0.41, 0.40, None], FRAC),
            series("system.mem.pct_usable", "host:web-2", [0.06, 0.05], FRAC),
        ],
    ),
    "query_disk_used.json": resp(
        "avg:system.disk.used{env:production} by {host,device}.rollup(avg, 15)",
        ["host", "device"],
        [
            series("system.disk.used", "device:/dev/sda1,host:web-1", [40 * G] * 3, BYTE),
            series("system.disk.used", "device:/dev/sdb1,host:web-1", [79 * G, 80 * G], BYTE),
            series("system.disk.used", "device:/dev/sda1,host:web-2", [10 * G], BYTE),
        ],
    ),
    "query_disk_total.json": resp(
        "avg:system.disk.total{env:production} by {host,device}.rollup(avg, 15)",
        ["host", "device"],
        [
            series("system.disk.total", "device:/dev/sda1,host:web-1", [100 * G] * 3, BYTE),
            series("system.disk.total", "device:/dev/sdb1,host:web-1", [100 * G] * 2, BYTE),
        ],
    ),
}

for name, body in out.items():
    with open(sys.argv[1] + "/" + name, "w") as f:
        f.write(json.dumps(body, indent=1) + "\n")
