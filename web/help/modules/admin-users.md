---
id: admin-users
title: Users
routes: [/admin/users]
terms: [role, tenant, api-key]
diagrams: []
---

A person's access is the roles they hold in a tenant. The role decides
what they can do: an admin configures the tenant, a GRC engineer authors
controls and policies, a control owner looks after the controls they own,
an auditor reads what their audit period covers, and a viewer reads
without changing anything. One person can hold several roles. The
role-permission matrix at the bottom of the page spells out each one.

## What this page shows

Every user in the tenant, with their roles. Signing in through SSO tells
security-atlas who someone is; this page decides what they may do.

## Common tasks

- **Add a user** and tick their roles. On an install with several
  tenants the button is **Assign user to tenant**, and you pick the
  tenant too.
- **Add me to a tenant**, on an install with several tenants, gives you
  a role in another tenant, so you can
  work in it. Sign in again afterwards for it to take effect.
- **Revoke roles** removes every role a user holds in the tenant. A
  tenant's only active admin can't be revoked, so the tenant is never
  left without one. Revoking roles doesn't revoke API keys the person
  issued; the dialog lists them so you can revoke them on the API keys
  page.

## Who can do this

An admin of the tenant, or a super admin. A tenant's admins see and
change only their own tenant's members, and can give any of the five
roles, admin included. Super admin isn't a role, so it can't be given
here. Giving someone a role in a different tenant, and **Add me to a
tenant**, need a super admin.
