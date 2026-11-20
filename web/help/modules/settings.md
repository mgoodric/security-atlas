---
id: settings
title: Settings
routes: [/settings]
terms: [tenant, api-key, self-approved, role]
diagrams: []
---

Settings holds your own preferences: how the app looks, what it tells
you about, and where you are signed in. Anything you change here affects
only you, apart from the organisation section, which applies to the
whole tenant.

## What each section does

- **Profile** shows your name, email and roles. Your name and email come
  from your identity provider, so you change them there. You can set your
  time zone.
- **Appearance** picks a light or dark theme, or follows your system.
- **Onboarding hints** brings back every "New to …?" line you have
  dismissed, on every browser you use.
- **Notifications** chooses which events reach you in the app and by
  email, and turns on a daily email digest if you want one.
- **Active sessions** lists the browsers you are signed in on. Revoke any
  you don't recognise; the one you are using stays signed in.
- **Personal API tokens** issues, rotates and revokes API keys. An admin
  of the tenant or a super admin can use it. A tenant's admins can't
  issue a key with admin privileges or one that approves, and can't
  rotate another person's personal key, only revoke it.
- **Organisation** renames the tenant and sets its defaults: how control
  bundles are checked on upload, how often risks are reviewed, and when a
  new vendor's first review is due. An admin of the tenant or a super
  admin sees it, and every change is recorded in the audit log.

## Self-approval

Approvals normally need a second person: whoever asked for a policy,
framework scope, exception or risk acceptance can't approve it too. A
small team can turn on **Allow self-approval** for the organisation, so
the requester may approve their own record. Only a tenant admin or a
super admin can change it, today through the API rather than this page,
and each change is written to the audit log. A record approved this way
is marked **Self-approved** everywhere it appears. An API key never
approves, whatever the setting.
