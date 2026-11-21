# policies/

[Open Policy Agent](https://www.openpolicyagent.org) Rego policies used for:

- **Authorization** (RBAC + ABAC) — every API request is decided by OPA per slice 035
- **Control evaluation** — control evidence queries are Rego expressions over the evidence ledger per slice 012

Both use the same embedded OPA Go library — same engine evaluates control queries and authz decisions, so the security model is auditable in the same substrate as the controls.

## Where the Rego lives

- **Authorization policies** are in `internal/authz/rego_bundle/`, compiled
  into the binary with `//go:embed` (`internal/authz/decision.go`). That
  directory is the only copy. Edit it there; there is no mirror to sync
  (#2677 removed the `policies/authz/` copy).
- **`stock/`** holds the stock policy documents (Markdown), not Rego.
