---
id: admin
title: Admin
routes: [/admin]
terms: [super-admin, tenant, sso, api-key, module]
diagrams: []
---

Admin is where the install itself is configured: how people sign in, who
has which role, which programs can call the API, and which parts of the
product are switched on. Day-to-day programme work happens elsewhere.

## Who can open it

Only a super admin can open the Admin pages. A role of admin inside a
tenant is not enough on its own, and anyone else sees a page explaining
that they need a super admin.

## What is here

- **SSO** connects your identity provider, so people sign in with their
  work accounts.
- **Users** lists who belongs to the tenant and which roles they hold.
- **API keys** issues the credentials that connectors and the
  command-line tool use.
- **Features** switches optional modules on or off for the tenant.
- **Audit** opens the activity log, the record of who changed what.
- **Super admins** and **Tenants** appear once the install holds more
  than one tenant.
- **Demo** appears when the install has demo data turned on.

## Common tasks

- **Set up a new install**: connect SSO first, then add the people who
  need access and give each the narrowest role that fits.
- **Check what changed** in the audit log when something looks different.
