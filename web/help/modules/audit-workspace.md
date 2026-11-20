---
id: audit-workspace
title: Audit workspace
routes: [/audit]
terms: [assigned-auditor, evidence-horizon, population, sample, walkthrough]
diagrams: []
---

The audit workspace is where an external auditor tests your controls for
one audit period. It opens on the most recently started period you are
assigned to. The bar across the top names that period, its dates, and
whether it is frozen.

## The evidence horizon

While a period is open, sampling draws from current evidence. Once an
admin freezes it, the moment of freezing becomes the period's evidence
horizon: populations and samples hold only evidence observed up to then.
Your live programme carries on; the auditor's view stops moving.

## Testing a control

Pick a control on the left, or add one by its id. Each control has three
tabs:

- **Sampling**: the populations built for this control in the period.
  Draw a sample by choosing a size and a seed; the same seed always picks
  the same records. Record each record as passed, failed or not
  applicable, with a note.
- **Walkthrough**: a recorded explanation of how the control runs, with
  an optional attachment.
- **Comments**: notes on the control. A private note is seen only by its
  author. A shared note reaches your organisation's team, who can reply.

A half-written result survives moving between tabs and controls.

## Who can do what

- **See a period here**: an auditor assigned to it by an admin. Anyone
  else sees "No audit period assigned".
- **Draw samples and record results**: assigned auditors, in their own
  period, plus admins and GRC engineers.
- **Build a population**: admins and GRC engineers.
- **Record a walkthrough**: admins and GRC engineers. Auditors read them.
- **Write comments**: assigned auditors, private or shared. GRC
  engineers reply on shared notes and never see private ones.
