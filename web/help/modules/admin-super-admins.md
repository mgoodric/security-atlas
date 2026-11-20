---
id: admin-super-admins
title: Super admins
routes: [/admin/super-admins]
terms: [super-admin, tenant, role]
diagrams: []
---

A super admin looks after the install as a whole rather than one
tenant. Super admins open every Admin page, including the install-wide ones,
create tenants, and grant or demote other super admins. Super admin isn't a role inside a tenant: on
its own it gives no right to read or change a tenant's programme. To
work in a tenant, a super admin still needs a role there, given on the
Users page.

The first administrator of a new install becomes its first super admin.

## Common tasks

- **Grant super admin** to a user in this tenant, by picking them or
  pasting their user ID. Granting it to someone who already has it
  changes nothing.
- **Demote** a super admin from their row, and confirm.

The last remaining super admin can't be demoted, so an install is never
left with nobody able to administer it. Grant a second super admin
before the first one steps down.

## Who can do this

Only super admins.
