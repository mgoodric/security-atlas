---
id: risks-hierarchy
title: Risk hierarchy
routes: [/risks/hierarchy]
terms: [org-unit, theme, roll-up, decision]
diagrams: []
---

The register is a flat list. The risk hierarchy shows the same risks the
way your organisation is built, so you can see where risk sits and spot
the same problem turning up in several places.

## The pieces

- **Org units** are the parts of your organisation, such as a business
  unit, a department or a team, arranged as a tree. Each risk can belong
  to one, at a level: team, org or company.
- **Themes** are tags that cut across units, such as access control or
  tech debt. A theme seen in several teams is often one larger risk.
- **Roll-ups** are risks that sum up others linked under them, each at a
  lower level than the roll-up. A roll-up's severity is worked out from its children each time you look,
  and it is flagged for review when the children add up to more than the
  roll-up was given. Roll-ups are left out of register counts so nothing
  is counted twice. Closed risks drop out of roll-ups.
- **Decisions** record a trade-off someone made, such as deferring a
  piece of work, with who decided and when to look at it again.

## What this page shows

Three panels. The org tree lists your units with their levels; open a
unit to see the units under it. The theme heatmap lays themes against units, but the
count for each cell is not available yet, and the panel says so rather
than show made-up numbers. The decision timeline lists decisions, newest
first, and marks any whose revisit date has passed. That marker stays
until someone acts on the decision.

## Who can do what

- **Read** the hierarchy: anyone who can read the register.
- **Change** org units, themes and roll-up links: admins and GRC
  engineers.
- A decision is always recorded by a person, who is named as its maker.
  Nothing here creates one for you, and this page only reads them.
