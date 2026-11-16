# Bundled crosswalks

Each `*.yaml` file here maps one framework's requirements to SCF anchors. The
files are embedded in the `atlas-cli` binary (`embed.go`): the SCF workbook
importer takes requirement titles and bodies from the bundled file for the
same framework slug (see `docs/operator/scf-crosswalk-import.md`, "Requirement
titles"). Every release therefore ships their contents, not just the repo.

## Authoring rules

- **Never quote a copyrighted standard.** For ISO/IEC 27001, PCI DSS and the
  AICPA SOC 2 Trust Services Criteria, a requirement carries its identifier,
  its title, and a `body` written in our own words. Do not paste clause,
  control or criterion text from the standard, not even in a comment.
- **Public-domain sources may be quoted.** NIST publications (such as CSF 2.0)
  and U.S. regulation (such as the HIPAA Security Rule, 45 CFR Part 164) are
  not under copyright, so a `body` may reproduce the source text.
- **Check the license before adding a framework.** A standard not named here
  is treated as copyrighted until its license says otherwise.
- **The licensing posture in each file's header applies to everything
  shipped.** Read it before adding or changing a requirement, and keep new
  rows within it. A framework whose license forbids bundling (CCM, CAIQ, SIG)
  does not get a file here; it is imported from a customer-supplied file.
- **Map requirement to SCF anchor only**, never requirement to requirement
  (invariant #7).
- **Record each mapping call** in the decisions log the file header names,
  under `docs/audit-log/`.
- **A frozen framework version keeps its stored text.** Changing a title or
  body of an already-imported version has no effect on existing
  installations; ship the change as a new `framework_version`.
