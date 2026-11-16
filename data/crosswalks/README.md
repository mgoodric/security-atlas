# Bundled crosswalks

Each `*.yaml` file here maps one framework's requirements to SCF anchors. The
files are embedded in the `atlas-cli` binary (`embed.go`): the SCF workbook
importer takes requirement titles and bodies from the bundled file for the
same framework slug (see `docs/operator/scf-crosswalk-import.md`, "Requirement
titles"). Every release therefore ships their contents, not just the repo.

## Authoring rules

- **Never put verbatim standard text in a crosswalk.** A requirement carries
  its identifier, its title, and a `body` written in our own words. Do not
  paste clause, control or criterion text from the standard, even for a
  public-domain framework, and even in a comment.
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
