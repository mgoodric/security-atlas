---
id: admin-demo
title: Demo tools
routes: [/admin/demo]
terms: [tenant, multi-tenant, super-admin]
diagrams: []
---

The demo tools fill a separate tenant, called demo, with a realistic
sample programme, so you can try security-atlas or show it to someone
without touching real records. The sample holds about 50 controls,
20 risks, 200 evidence records, 3 audit periods and 12 audit samples.

## When they are available

Demo data is off unless whoever runs the deployment turns it on, by
setting `ATLAS_ENABLE_DEMO_SEED=true`. Until then this page explains how,
and its buttons are hidden. Because the demo is a tenant of its own,
adding it beside your organisation also needs the install opted in to
multi-tenant.

## Common tasks

- **Reseed demo dataset** creates the demo tenant, or refreshes it to
  its starting state. It asks you to confirm first.
- **Tear down demo tenant** deletes the demo tenant and everything in
  it. It only ever deletes the tenant marked as the demo, never your own.

Each action can run once a minute, and each is written to the audit log
before it runs.

## Who can do this

Only super admins.
