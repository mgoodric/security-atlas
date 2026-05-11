# Slice 027 — Walkthrough recording: annotated capture + transcript + hash/sign

Copy-paste prompt for executing slice 027. See `Plans/prompts/04-per-slice-template.md` for the template that generated this file.

---

```
Build docs/issues/027-walkthrough-recording.md.

Branch: audit/027-walkthrough-recording (from main).

Workflow:
1. Read CLAUDE.md constitutional invariants + the issue's "Constitutional invariants honored" + "Canvas references" sections
2. grill-with-docs the issue against the cited canvas section(s) — flag drift before coding
3. tdd loop per acceptance criterion (integration > unit; never mock the DB; never test private methods)
4. database-designer for any migration (idempotent + reversible)
5. security-review on any PR touching auth, ingest, RLS queries, or external IO
6. simplify pass before opening PR
7. ship-gate must pass before claiming done
8. changelog-generator entry for the slice

Honor every anti-criterion (P0 — block merge).

Respect CLAUDE.md style (no emojis, Conventional Commits, Co-Authored-By trailer).

Open PR titled "feat(audit): Walkthrough recording: annotated capture + transcript + hash/sign (#027)" with body: AC pass/fail · files changed · CI URL · open questions surfaced.

Use Algorithm mode. Initialize a PRD (id: 027-walkthrough-recording).
```

---

**Quick reference for this slice:**

- Issue file: [`docs/issues/027-walkthrough-recording.md`](../../../docs/issues/027-walkthrough-recording.md)
- Cluster: `audit`
- Branch: `audit/027-walkthrough-recording`
- PRD id: `027-walkthrough-recording`
