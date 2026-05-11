# policies/

[Open Policy Agent](https://www.openpolicyagent.org) Rego policies used for:

- **Authorization** (RBAC + ABAC) — every API request is decided by OPA per slice 035
- **Control evaluation** — control evidence queries are Rego expressions over the evidence ledger per slice 012

Both use the same embedded OPA Go library — same engine evaluates control queries and authz decisions, so the security model is auditable in the same substrate as the controls.

Empty in slice 001.
