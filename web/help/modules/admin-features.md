---
id: admin-features
title: Features
routes: [/admin/features]
terms: [module, switched-off, tenant]
diagrams: []
---

security-atlas is made of modules. The core ones, such as Controls,
Evidence, Frameworks and Audit periods, are always on, because the rest
of the product depends on them. The optional ones, such as Risk,
Policies, Questionnaires and Exceptions, can be switched off for a
tenant that doesn't use them. Inside a module, smaller features can be
switched on and off too, while the module itself is on.

## What switching off does

A switched-off module disappears for everyone in the tenant: its pages
leave the sidebar, and anyone who follows an old link sees a short note
that the module is off. Nothing is deleted. The records, their history
and the module's feature settings stay as they were, and switching the
module back on brings them all back.

## Common tasks

- **Enable** a module or feature. You can add a reason, which is kept
  with the change.
- **Disable** one from its row menu, and confirm. A reason is optional
  here too.

Core modules show **Core module, always on** and have no switch.

## Who can do this

Only super admins. Other people who reach a switched-off page are told
to ask an admin.
