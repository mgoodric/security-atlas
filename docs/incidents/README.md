# Incident logs

This directory holds the per-incident logs the project produces under
[`docs/governance/incident-response.md`](../governance/incident-response.md).
One file per incident, named `YYYY-MM-DD-<slug>.md` where the date is
the detection date.

The incident response plan documents the templates, severity tiers,
roles, and workflow that govern what lands here. Read it first.

---

## File naming

`docs/incidents/YYYY-MM-DD-<slug>.md`

Examples (illustrative — not real incidents):

- `docs/incidents/2026-06-15-pat-leak-feature-branch.md`
- `docs/incidents/2026-08-02-dependabot-high-cvss-go-yaml.md`
- `docs/incidents/2026-11-10-ghcr-push-401-investigation.md`

---

## Status visibility

The frontmatter of each incident log carries a `status` field with
values `open`, `contained`, `eradicated`, `resolved`, or
`not-promoted`. To list active incidents:

```bash
# Replace with your preferred tool — this is illustrative.
grep -l 'status = "open"' docs/incidents/*.md 2>/dev/null
grep -l 'status = "contained"' docs/incidents/*.md 2>/dev/null
```

A future slice may automate this into a maintainer-facing dashboard;
for now the file system is the database.

---

## Confidentiality

Incident logs are public-by-default. Where attack-vector detail must
be redacted for safety, the public log carries a `[redacted]`
placeholder and the unredacted material is held privately by the
maintainer (per
[`docs/governance/incident-response.md`](../governance/incident-response.md)
§8 "Confidentiality").

---

## Cadence expectations

A small number of incidents per year is the realistic baseline. If
the cadence exceeds **12 per year**, that is itself a signal worth
investigating — either the detection surface has been ratcheted up
or the project is under unusual stress, and the maintainer reviews
why at the next quarterly governance checkin.

---

## Template

See [`docs/governance/incident-response.md`](../governance/incident-response.md)
§10 "Incident log template" for the full Markdown shape. The
post-incident review template is at §9 of the same document.
