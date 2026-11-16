---
id: admin-tenants
title: Tenants
routes: [/admin/tenants]
terms: [tenant, multi-tenant, super-admin, role]
diagrams: []
---

A tenant is one organisation's separate space: its own controls, risks,
evidence, people and settings. Nothing in one tenant can be seen from
another. Most installs need only one, and the app calls it your
organisation. An install holds several tenants when, for example, a
consultant runs security programmes for several clients.

## Opting in to multi-tenant

A new install has one tenant, and creating a second is refused until
whoever runs the deployment opts in. They set
`ATLAS_ALLOW_MULTI_TENANT=true` in the install's configuration and
restart it. A super admin can then create the second tenant here. This
page stays reachable by its address while the install has one tenant,
even though its link is hidden.

Once a second tenant exists, the tenant picker appears in the header,
and the Tenants and Super admins links appear in Admin.

## Common tasks

- **Create tenant** with a name and a short slug. Tick **Join as admin**
  to give yourself the admin role in it, or give someone else a role on
  the Users page afterwards. A new tenant starts with a default scope, so
  it is ready for controls and evidence straight away.

Tenants can't be deleted from the app.

## Who can do this

Only super admins.
