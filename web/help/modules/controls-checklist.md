---
id: controls-checklist
title: Implementation checklist
routes: [/controls/checklist]
terms: [implementation-checklist, owner-role]
diagrams: []
---

Knowing which controls apply is not the same as knowing who has to do
what. The implementation checklist turns your in-scope controls into a
to-do list for each team, so you can hand infrastructure, engineering and
security their share of the work.

## How it is built

Each control goes to a team by its owner role. That split follows a fixed
rule, not the AI, so the same controls always land with the same team.
Controls whose owner role matches no team are listed under
**Unassigned**, so nothing drops out of the list.

The AI then writes the concrete tasks for each team, and every task cites
the control it comes from. A section whose tasks cannot all be traced to
a real control is held back rather than shown. If the AI is unavailable,
each team's section says so and you can generate again; the Unassigned
list still appears.

Your organisation's AI runs on your own deployment by default. If your
organisation has opted into a cloud model instead, the page says so, because
your control text then leaves your deployment.

## A draft until you approve it

The checklist is marked as an AI-assisted draft. Review each team's
section and approve it with one click; nothing is approved for you.
**Export** downloads the approved sections as Markdown, and stays off
until at least one section is approved. Treat the checklist as a working
plan, not as audit evidence.

## Who can do what

- **Generate** a checklist and **approve** its sections: admins and GRC
  engineers.
- **Read and export** a generated checklist: the same people, and anyone
  who holds a control owner role.
