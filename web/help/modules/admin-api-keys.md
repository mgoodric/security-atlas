---
id: admin-api-keys
title: API keys
routes: [/admin/api-keys]
terms: [api-key, role]
diagrams: []
---

An API key lets a program act without a person signing in: a connector
pushing evidence, a script, or the command-line tool. Treat its secret
like a password. security-atlas shows the secret once, when the key is
issued or rotated, and never again, so copy it into your secret store
straight away.

## What a key can do

A key issued from this page acts as a GRC engineer, or as an admin when
you tick **Grant admin privileges on this credential**. You can narrow a
key to a scope and to the kinds of evidence it may push. A key never
approves anything, so approvals always need a person.

A key bound to a person, rather than to a program, uses that person's
current roles instead. When their roles are revoked or their account is
turned off, the key loses its access too. Keys issued from this page are
program keys.

## Common tasks

- **Issue a key**: set how many days it lasts (90 by default), then
  **Issue credential** and copy the secret.
- **Rotate** a key before it expires, or when its secret may have leaked.
  You get a new secret, and the old one keeps working for a grace period,
  7 days by default, so you can update the program without a gap.
- **Revoke** a key to stop it at once. Revoke the old key after a
  rotation once nothing uses it.

## Who can do this

Only super admins, here or under **Personal API tokens** in Settings.
