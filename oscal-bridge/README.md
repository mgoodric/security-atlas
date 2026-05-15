# oscal-bridge/

Python service wrapping IBM [`compliance-trestle`](https://github.com/oscal-compass/compliance-trestle)
for OSCAL JSON v1.1.x serialization. Bridged from Go (`cmd/atlas-oscal/` +
`internal/oscal/`) via gRPC. Landed in slice 030 (OSCAL SSP + POA&M export).

## What it does

The Go platform aggregates a **frozen** `AuditPeriod`'s data — org profile,
scope cells, control implementations, linked policies, sample populations,
walkthroughs, audit notes, failing control evaluations — into the protobuf
input messages defined in `proto/oscal/v1/oscal.proto`. It then calls this
service over gRPC. The bridge maps those aggregates to canonical OSCAL JSON
v1.1.x documents (SSP, Assessment Plan, Assessment Results, POA&M) via
`compliance-trestle` and round-trip-validates them.

The bridge is intentionally **stateless and thin**: no database, no auth,
no business logic, **no LLM**. All freezing-horizon enforcement and tenant
isolation happen on the Go side before a request reaches this contract. SSP
narrative text arrives pre-authored — the product-runtime AI-assist boundary
(`CLAUDE.md`) is structurally preserved here by the absence of any inference
client.

## Layout

| Path                               | Purpose                                                |
| ---------------------------------- | ------------------------------------------------------ |
| `atlas_oscal_bridge/serializer.py` | proto input -> trestle OSCAL models -> canonical JSON  |
| `atlas_oscal_bridge/server.py`     | gRPC server binding `OscalBridgeService`               |
| `atlas_oscal_bridge/oscal_pb2*.py` | generated gRPC stubs (do not hand-edit)                |
| `scripts/gen_proto.sh`             | regenerate the stubs from `proto/oscal/v1/oscal.proto` |
| `tests/`                           | pytest unit + in-process gRPC server tests             |

## Develop

```sh
cd oscal-bridge
uv sync --extra test
# regenerate gRPC stubs after a proto change
bash scripts/gen_proto.sh
# run the service
python -m atlas_oscal_bridge.server --address 127.0.0.1:50070
# test
PYTHONPATH=. python -m pytest tests/ -q
```

Lint + format are driven from the repo root: `just lint-python` / `just fmt-python`
(ruff). The generated `oscal_pb2*.py` stubs are excluded from ruff in the root
`pyproject.toml`.

## OSCAL version

trestle 4.0.x ships pydantic models whose `OSCAL_VERSION` constant is `1.2.1`
(a superset of the 1.1.x line). The bridge **stamps every emitted document**
with `oscal-version: "1.1.2"` — the version security-atlas commits to in
canvas §3.4. See `docs/audit-log/030-oscal-ssp-poam-export-decisions.md` for
the rationale and the "validate against a real auditor's tooling" revisit
item.
