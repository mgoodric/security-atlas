# ADR 0021 — Security software commercials extend the vendor register

**Status:** Accepted.

**Date:** 2026-07-30

**Issue:** OPENENGINE-623.

## Context

Security teams already track many purchased tools as third parties: Okta,
CrowdStrike, Datadog, scanners, SIEMs, IAM, and GRC systems are both vendors
and software the program depends on. The existing vendor register already owns
the tenant-scoped entity, contract dates, DPA status, review cadence, owner,
and upcoming review surfaces. What it lacked was the commercial data operators
usually keep in a spreadsheet: annual cost, currency, renewal date,
auto-renew, licenses/seats, tool category, cost owner, status, and billing
cadence.

## Decision

Extend `vendors` with optional commercial/software fields and add a
software/tooling lens over the existing vendor list. Contract renewal events
use the new `renewal_date` field and are surfaced next to the existing vendor
review upcoming events. Spend rollups aggregate entered annualized cost by
currency overall and by tool category.

## Alternative Considered

A separate `software` or `tooling` primitive would first-class open-source or
internal tools that are not third-party vendors. That was rejected for this
slice because the stated need is contract and commercial management, and most
contracted security tools are vendors already. A parallel primitive would
duplicate contract dates, ownership, tenant isolation, list/detail workflows,
and upcoming surfaces before there is a concrete first-class non-vendor tooling
requirement.

## Consequences

- Vendor risk review and DPA workflows remain intact; the new commercial fields
  are additive and optional.
- Tenant isolation stays at the existing vendor table's RLS boundary.
- Multi-currency spend is not collapsed into one misleading number. Rollups are
  grouped by currency.
- If internal or open-source tooling later needs lifecycle management without a
  vendor relationship, revisit a distinct tooling primitive with an explicit
  migration path from vendor-linked tools.
