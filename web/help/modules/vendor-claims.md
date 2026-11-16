---
id: vendor-claims
title: Vendor claims
routes: [/oscal/component-definitions]
terms: [vendor-claim, oscal, anchor]
diagrams: []
---

Some vendors publish what their product does for your security
programme as an OSCAL component definition: a file listing controls and,
for each, the vendor's statement of how the product meets it. Each of
those statements is a vendor claim. A claim is the vendor's word, not
evidence security-atlas has checked, so it never makes one of your
controls pass on its own.

## How claims arrive

An admin or GRC engineer imports the vendor's file with the `atlas-oscal`
command-line tool. The file is checked against the OSCAL standard first,
and nothing is saved if it fails. Each claim is matched to an SCF anchor
where possible; the rest are marked **Unmapped to SCF**.

## Reviewing claims

Open an import to see its claims. For each one:

- **Accept** it when you are willing to rely on the vendor's statement.
  An accepted claim is included, labelled as vendor-attested, in your
  OSCAL security plan export.
- **Reject** it when you don't rely on it.
- **Needs info** when you have asked the vendor for more.

You can add a note to each decision, and the row then shows who decided
and why. Rejected claims and claims that need information are left out of
exports. For an unmapped claim, pick the SCF anchor it belongs to; that
records the mapping but satisfies nothing on its own.

## When it is available

Vendor claims are part of the optional Third-party risk module.

## Who can do this

Admins and GRC engineers can open vendor claims. Accepting, rejecting and
mapping claims needs the approver right.
