---
id: controls
title: Controls
routes: [/controls]
terms:
  [anchor, crosswalk, owner-role, attestation, evidence-freshness, excepted]
diagrams: []
---

A control is one thing your organisation does to stay secure, such as
"every account signs in with multi-factor authentication". You write each
control once. Frameworks such as SOC 2 and ISO 27001 take credit from it
through its SCF anchor, so one control can satisfy requirements in several
frameworks without being copied for each.

## What this page shows

The **View** filter switches between two lists:

- **My controls** lists your organisation's controls, each with its
  current state and how fresh its evidence is.
- **SCF catalog** lists the controls of the Secure Controls Framework
  (SCF), with the controls you have mapped to each one. Filter it to
  **No control** to find gaps: an SCF control with nothing mapped to it
  earns no framework credit.

The other filters narrow either list, and you can save a set of filters
as a view to come back to.

## How a control gets its state

A control's state comes from its evidence, not from how the control is
described:

1. Evidence arrives, from a connector or from a person attesting to it.
2. Each evaluation reads the control's current evidence and works out a
   state: **pass**, **fail**, **insufficient_evidence** or
   **not_applicable**. Any failing check makes the control fail.
3. Evidence ages from fresh to stale to expired. Expired evidence no
   longer counts, so a control without current evidence cannot keep
   passing on old results.

A control covered by an approved exception shows **Excepted** instead.

Manual controls work the same way as automated ones. The only difference
is where the evidence comes from: the control's owner uploads it or
attests to it on the control's page.

## Common tasks

- **Open a control** to see its evidence, the frameworks it satisfies,
  its owner and its history.
- **Attest to a manual control** from its page when you are its owner.
- **Select several controls** to act on them together.
