# oscal-bridge/

Python service wrapping IBM [`compliance-trestle`](https://github.com/oscal-compass/compliance-trestle) for OSCAL JSON v1.1.x serialization. Bridged from Go (`cmd/atlas-oscal/`) via gRPC.

Real bridge lands in slice 030 (OSCAL SSP + POA&M export). For slice 001, this directory contains only a minimal `pyproject.toml` so the `uv` workspace recognizes it.
