---
id: evidence
title: Evidence
routes: [/evidence]
terms: [evidence-ledger, attestation, evidence-freshness, evaluation]
diagrams: []
---

Evidence is the proof that a control is working: a scan result, a
configuration read from a cloud account, an access review someone
finished. Your controls are judged on their evidence alone, so this is
the record an auditor will ask to see.

## How evidence arrives

Most evidence comes from connectors, small programs that read a system
such as a cloud account or an identity provider and send what they see.
The rest comes from people: someone who holds a manual control's owner
role, or an admin, attests to it on the control's page and can attach a
file as proof.

Every record is kept in the evidence ledger. Records are added, never
edited or deleted, so the ledger always shows what you knew and when.
Evaluation reads the ledger to work out each control's state, and stays
separate from it: a mistake in evaluation is fixed by evaluating again,
never by changing evidence.

Evidence also ages. Each control's freshness class sets how long its
evidence counts. Older evidence stays in the ledger but no longer counts
towards the control's state.

## What this page shows

The ledger for your whole organisation, newest first. Each row shows
when the thing was observed, the kind of evidence, its result, its
source (a connector or a person), the part of your scope it covers and
the start of its fingerprint (hash); click the hash to copy all of it.
Filter by control, kind, result, source, scope or date.

## Who can do what

- **See evidence**: admins, GRC engineers and control owners. Auditors
  see only evidence for the audit periods they are assigned to. Viewers
  see freshness counts, not records.
- **Send evidence** from a connector or the command line: **Push
  evidence** explains how. This page itself never changes the ledger.
