---
id: vendors
title: Vendors
routes: [/vendors]
terms: [vendor-criticality, review-cadence, dpa]
diagrams: []
---

The vendor register lists the outside companies your organisation relies
on: cloud providers, SaaS tools, contractors. A vendor that holds your
data or runs part of your product is a risk you don't control directly,
so frameworks such as SOC 2 and ISO 27001 expect you to know who your
vendors are and to review them regularly.

## Criticality and reviews

Each vendor has a criticality (low, medium or high) and a review
cadence: monthly, quarterly, every six months or yearly. A vendor's next
review is due one cadence after its last one. A vendor that has never
been reviewed is due its first review a set time after it was added, 30
days unless your organisation changed it. Past that date the vendor is
overdue, and the list's review burndown counts it.

**Record review** on a vendor's page logs the outcome: pass, pass with
findings, fail, or waived, with notes. Reviews are a permanent record:
they can be added but never edited or deleted.

## What else the register holds

Contract and renewal dates, whether a data processing agreement is
signed, cost and seats, and the vendor's owner. The list can be filtered
by criticality, category or overdue reviews, and exported as CSV, JSON
or xlsx.

## Who can do this

Admins and GRC engineers add, edit and delete vendors and record
reviews. Viewers can read the register. Control owners and auditors
don't see it.
