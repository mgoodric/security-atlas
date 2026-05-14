"""atlas-oscal-bridge — OSCAL serialization bridge for security-atlas (slice 030).

The Go platform aggregates a frozen AuditPeriod's data into the protobuf
input messages defined in ``proto/oscal/v1/oscal.proto`` and calls this
service over gRPC. The bridge maps those aggregates to canonical OSCAL
JSON v1.1.x documents via IBM ``compliance-trestle`` and round-trip
validates them.

The bridge owns no business logic, no database access, and no LLM call.
SSP narrative text arrives pre-authored in the input messages — the
product-runtime AI-assist boundary (CLAUDE.md) is enforced on the Go
side and structurally preserved here by the absence of any inference
client.
"""

# The OSCAL version security-atlas commits to (canvas §3.4). trestle's
# own OSCAL_VERSION constant is a superset; we stamp every emitted
# document with this pinned value. See the slice-030 decisions log.
OSCAL_VERSION = "1.1.2"

__all__ = ["OSCAL_VERSION"]
