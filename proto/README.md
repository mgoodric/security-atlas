# proto/

gRPC protobuf definitions for cross-language contracts.

Shipped surfaces:

| File                                   | Purpose                                                                | Slice |
| -------------------------------------- | ---------------------------------------------------------------------- | ----- |
| `proto/evidence/v1/evidence.proto`     | `EvidenceIngestService.Push` RPC + `EvidenceRecord` message            | 003   |
| `proto/connectors/v1/connectors.proto` | `ConnectorRegistryService.Register` + `List` (connector self-announce) | 004   |
| `proto/admin/v1/credentials.proto`     | Admin API for push-credential issuance and rotation                    | —     |
| `proto/oscal/v1/oscal.proto`           | OSCAL bridge contract (Go ↔ Python)                                   | 030   |

**Wire reality (see [`Plans/EVIDENCE_SDK.md`](../Plans/EVIDENCE_SDK.md) §3):** the evidence ingest surface is a single `Push(record) → Receipt` RPC. The platform does **not** expose a connector-side `Pull` / `Subscribe` / `Describe` / `AuthMethods` / `HealthCheck` / `ListEvidenceKinds` / `VerifyProvenance` RPC — those are per-connector in-process concerns. The connector-management RPCs are limited to `Register` (connector self-announces at startup) and `List` (operator introspection); the `profiles_supported []string` field on `RegisterRequest` is operator-facing metadata describing the connector's source-side fetch direction, not a platform-side wire dimension.
