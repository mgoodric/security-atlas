# control testdata

This directory holds bundle fixtures used by parser and integration tests.
Each subdirectory is a valid (or deliberately invalid) bundle layout.

| Fixture              | Purpose                                              |
| -------------------- | ---------------------------------------------------- |
| `minimal-bundle/`    | Smallest valid bundle — manifest only.               |
| `aws-mfa-bundle/`    | Full bundle: applicability_expr, evidence_queries.   |
| `manual-bundle/`     | manual_periodic implementation_type + manual schema. |
| `no-anchor-bundle/`  | Missing scf_anchor_id — must reject (AC-4).          |
| `bad-applicability/` | Invalid applicability_expr operator (AC-5).          |
