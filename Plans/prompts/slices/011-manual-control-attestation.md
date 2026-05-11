# Slice 011 — Manual control type + attestation/upload flow

Copy-paste prompt for executing slice 011. See `Plans/prompts/04-per-slice-template.md` for the template that generated this file.

---

```
Build docs/issues/011-manual-control-attestation.md.

Branch: control-as-code/011-manual-control-attestation (from main).

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

Open PR titled "feat(control-as-code): Manual control type + attestation/upload flow (#011)" with body: AC pass/fail · files changed · CI URL · open questions surfaced.

Use Algorithm mode. Initialize a PRD (id: 011-manual-control-attestation).
```

---

**Quick reference for this slice:**

- Issue file: [`docs/issues/011-manual-control-attestation.md`](../../../docs/issues/011-manual-control-attestation.md)
- Cluster: `control-as-code`
- Branch: `control-as-code/011-manual-control-attestation`
- PRD id: `011-manual-control-attestation`
