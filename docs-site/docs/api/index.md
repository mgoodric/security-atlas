---
title: REST API reference
hide:
  - navigation
  - toc
---

# REST API reference

This page renders the OpenAPI 3.1 specification for the security-atlas
REST API. The source spec at
[`docs/openapi.yaml`][source] is the single source of truth — it is
generated deterministically from the in-tree route registrations by
[`cmd/atlas-openapi`][generator] and kept in lockstep with the running
code by a BLOCKING CI guard (`openapi-drift-check`).

[source]: https://github.com/mgoodric/security-atlas/blob/main/docs/openapi.yaml
[generator]: https://github.com/mgoodric/security-atlas/tree/main/cmd/atlas-openapi
[slice-140]: https://github.com/mgoodric/security-atlas/blob/main/docs/issues/140-openapi-spec-and-redoc-ui.md

!!! note "Two API surfaces"
The platform exposes two API surfaces. This page documents the
**REST** surface (operator integration, dashboards, admin
workflows). The **gRPC** surface (Evidence SDK push, connector
ingest, OSCAL bridge) is specified separately in the
[`proto/`](https://github.com/mgoodric/security-atlas/tree/main/proto)
files. The two surfaces stay separately specified.

!!! info "Internal endpoints are hidden from this render"
Operator-only probes (`/health`, `/metrics`, `/v1/version`,
`/v1/install-state`) carry `x-internal: true` in the source spec
and are filtered out at docs-build time before reaching this page
— see [slice 140][slice-140] P0-A3. They exist for docker-compose
healthchecks, Prometheus scraping, and SSR rendering; they are not
part of the consumer integration surface.

<!--
  Redoc standalone bundle. Loaded from jsdelivr CDN with subresource
  integrity (SRI) — zero bytes added to the deployed site (slice 140
  P0-A5 budget). The Redoc element auto-fetches the spec from the
  static URL declared in `spec-url`.

  The spec URL points at the BUILD-TIME-FILTERED copy that the
  `docs-site/hooks/openapi_pipeline.py` hook produces (operators-only
  endpoints stripped). The source `docs/openapi.yaml` at the repo
  root is the unfiltered single source of truth.

  `disable-search` keeps the page lighter — the mkdocs Material
  search already covers the surrounding pages, and operationId lookups
  are exposed via the Redoc left-nav.
-->

<div markdown="0">
<redoc spec-url="../openapi.yaml" hide-loading disable-search></redoc>
<script src="https://cdn.jsdelivr.net/npm/redoc@2.1.5/bundles/redoc.standalone.js" crossorigin="anonymous"></script>
</div>
