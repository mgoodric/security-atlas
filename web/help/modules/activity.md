---
id: activity
title: Activity
routes: [/activity]
terms: [activity-log, tenant]
diagrams: []
---

Every change in security-atlas leaves a record: who did it, what
changed and when. Together these records are the activity log. It is
how you answer "who approved this?" or "when did this control start
failing?", and it is what an auditor reads to trust everything else. The
log is append-only: nobody can edit or delete an entry, including
admins.

## What this page shows

By default, each event is a sentence, such as "Alex approved an
exception", grouped by day. Expand a row to see its details. Switch to
the raw ledger for the event's kind, its target and its identifiers,
which help when you match the log against an export or another system.

Filter by time window, by who acted, or by kind of event. The filters
are kept in the page's link, so you can share an exact view. Looking at
records is not shown unless you include reads, which keeps the feed to
changes. Your own sign-ins and account changes appear too.

The page is read-only. Viewing the log is itself recorded.

## Who can do what

- **See every event** in your organisation: GRC engineers, auditors and
  super admins.
- **Everyone else** sees the organisation's shared events, such as
  evidence, exceptions, decisions and audit periods, plus their own
  account activity. Changes to feature switches and other people's
  personal account events are hidden.
- **Export** the log as CSV, JSON or a spreadsheet: GRC engineers,
  auditors and super admins.
