---
id: scf-catalog
title: SCF catalog
routes: [/catalog/scf, /catalog/audited-under]
terms: [scf, anchor, crosswalk, strm, org-mapping]
diagrams: []
---

Every framework, from SOC 2 to ISO 27001 to PCI DSS, asks for much the
same things in different words. The Secure Controls Framework (SCF) is a
free catalog of about 1,400 security controls that sits in the middle:
each framework's requirements are mapped to SCF controls, called
anchors. Your own controls each point at one anchor, so one control can
satisfy matching requirements in every framework that maps to it.

## Browsing the catalog

The catalog lists every anchor by its SCF id, family and name, and can
be searched and exported. Open an anchor to see, framework by framework,
the requirements mapped to it. These published mappings are the
crosswalk. Each says how the requirement relates to the anchor (equal,
a subset, a superset, or overlapping) and how strongly. Mappings your organisation added itself are listed after them
and labelled as yours.

## Audited under

Sometimes an auditor tested a control against a requirement its anchor
doesn't reach in the crosswalk. **Audited under** lists those cases, per
control and requirement, with candidate anchors. **Add overlay edge**
records your own mapping from the requirement to the control's anchor,
with a relationship, a strength, a rationale and a source. The mapping
then counts towards that framework's coverage. It never changes the
published crosswalk, and it can be retired later.

## Who can do this

Everyone can browse the catalog. Admins and GRC engineers see the
Audited under page and add, retire, import or export your
organisation's own mappings.
