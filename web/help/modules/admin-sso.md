---
id: admin-sso
title: SSO
routes: [/admin/sso]
terms: [sso, tenant, role]
diagrams: []
---

Single sign-on lets people sign in to security-atlas with the work
account they already have, at an identity provider such as Okta, Google
or Entra ID. security-atlas never issues identities itself: it asks your
identity provider who someone is, over the OpenID Connect (OIDC)
standard. Each tenant has one identity provider.

Signing in proves who someone is; it doesn't decide what they can do.
Roles are still given on the Users page.

## Setting it up

1. **Run preflight** with your identity provider's issuer URL. It checks
   that security-atlas can reach the provider and shows what it found,
   without saving anything.
2. Register security-atlas as an application at your identity provider,
   using the redirect URL shown here.
3. Enter the client ID and client secret the provider gives you, and the
   email domains allowed to sign in, then **Save**.

The client secret is never shown again once saved. Leave the field blank
to keep the current one.

Some providers, such as Entra ID, don't say whether an email address is
verified. Tick the option to link users from those providers anyway only
if you trust your provider's directory.

## Who can do this

Only super admins.
