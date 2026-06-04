# 250 — Settings Profile section surfaces credential-bearer artifacts as user identity

**Cluster:** Frontend + Auth
**Estimate:** 0.5d
**Type:** JUDGMENT
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)
**Parent:** #204 (per-page UI parity audit fleet) — settings audit. Slice 154 settings-only audit assumed an OIDC-backed human user; this finding is class category-iii (data-bound surface that lies) for the case where the JWT is bound to a credential rather than a human.

## Narrative

Live `/v1/me` against `https://atlas-edge.home.gmoney.sh` with the
admin JWT at `/tmp/atlas-edge-admin-jwt` returns:

```json
{
  "user_id": "user:c0000000-0000-4000-8000-000000000001",
  "tenant_id": "00000000-0000-4000-8000-000000000001",
  "display_name": "API key ",
  "email": "",
  "idp_subject": "",
  "tenant_role": "admin",
  "time_zone": null,
  "is_admin": true,
  "owner_roles": ["admin"],
  "roles": []
}
```

The JWT here is bound to an `admin_credential` (API key), not an
OIDC-backed human. The `/v1/me` endpoint dutifully reports:

- `display_name: "API key "` (literal — note the trailing space)
- `email: ""` (empty)
- `idp_subject: ""` (empty)
- `time_zone: null`
- `roles: []` (empty; only `owner_roles: ["admin"]` is populated)

The Profile section of `/settings` (mockup lines 136-167; impl in
`web/app/(authed)/settings/page.tsx` ProfileSection circa lines
220-380) is **designed for an OIDC-backed human user**. It renders:

- Initials avatar derived from `display_name` (slice 154 F7)
- Email row with "(read-only · managed by IdP)" caveat
- OIDC subject row
- Tenant role badge + multi-role tail
- Time zone editor

For a credential-bearer JWT, those rows render as:

- Avatar initials: `"AP"` (from "API" — meaningless)
- `display_name`: "API key " (degenerate)
- Email: empty
- OIDC subject: empty
- Tenant role: admin (correct)
- Time zone: empty `<select>` value

**Why this matters:**

1. **Honesty.** The Profile section claims to display "your account"
   — but for a credential-bearer JWT, there is no human "account"
   to display. The fields aren't lies (they ARE the credential's
   metadata), but the UI's framing implies a human user.
2. **Mockup parity.** Mockup line 146 shows
   `Sam Rivera · sam.rivera@sentinellabs.example · OIDC subject
okta|00u4f2…`. The mockup assumes (and depicts only) the
   OIDC-human user shape.
3. **Identity confusion.** A user-impersonating-credential (admin
   bootstrap, service-account JWT bridge per slice 209) viewing
   `/settings` sees rows about a credential, not a person. This
   blurs which "you" the page refers to.
4. **CLAUDE.md primary-user definition** — the solo security
   leader running their own program — interacts with the platform
   as themselves (OIDC user), NOT via a service-account JWT. The
   credential-bearer case is a v2 / bootstrap / CI-integration
   case. The page's framing serves the primary persona at the cost
   of the secondary one.

**Three options to consider:**

1. **Banner + degraded display.** Detect credential-bearer at the
   page level (e.g. `email === ""` AND `idp_subject === ""`).
   Render a banner: "You are signed in as a credential
   (`API key …{last4}`). The fields below describe the credential.
   For your personal profile, sign in via your IdP."
   Keep the rows visible but accurate about what they represent.
   Lowest-risk option; ~30 LOC.
2. **Hide the section for credentials.** If credential-bearer is
   detected, render only the banner + skip the rows + tokens +
   sessions sections (irrelevant to a credential). Highest
   honesty; loses some affordance.
3. **Server-side typing.** Extend `/v1/me` wire shape with a
   `subject_kind: "user" | "credential"` enum field; render
   page differently based on this. Cleanest architecturally; ~40
   LOC backend + ~30 LOC frontend; requires a coordinated change.

The engineer chooses + records in the decisions log. Default
recommendation: **(1) banner + degraded display** — preserves
the existing UI for the dominant OIDC-human path, adds honesty
for the credential-bearer path. Composes cleanly with slice 209.

**This finding does NOT debug the v1.14.0 500-error class** (P0-A4
of slice 204). The fact that the JWT bound to a credential surfaces
as `display_name: "API key "` is independent of the 500-class.

## Threat model

| STRIDE                | Threat                                                                                                                                                                                                                                            | Mitigation                                                     |
| --------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------- |
| **S** Spoofing        | Could a credential-bearer user impersonate a real human user via the display? No — the credential's role is `admin`; the display is misleading, not authoritative. The actual authz remains role-based.                                           | n/a (this finding is about UI honesty, not authz).             |
| **I** Info disclosure | The page already shows the credential's `display_name` and `tenant_role`. The finding does NOT add or expose new data.                                                                                                                            | n/a.                                                           |
| **R** Repudiation     | If the audit log distinguishes "user X did Y" from "credential Z did Y", a credential-bearer signed in to `/settings` should be displayed as a credential, not a user. The audit-log invariant is unchanged by this finding (it's a UI-only fix). | AC-5 below: domain audit-log shape (slice 134) is not touched. |

**Verdict.** `no-mitigations-needed` beyond what the existing authz
layer already enforces. This is UI honesty, not access control.

## Acceptance criteria

- **AC-1.** Engineer picks option 1, 2, or 3 above and records the
  rationale + chosen path in
  `docs/audit-log/250-settings-profile-credential-bearer-decisions.md`.
- **AC-2.** When `/v1/me` returns `email === ""` AND `idp_subject ===
""` AND `display_name.startsWith("API key ")` (the v1 credential-
  bearer signature; engineer may refine), the Profile section
  renders an honesty banner identifying the bearer as a credential,
  NOT a human user.
- **AC-3.** The OIDC-human-user case (real `email`, real
  `idp_subject`) continues to render the existing Profile section
  unchanged — no regression in slice 154 F7's avatar / hero block.
- **AC-4.** The credential-bearer detection helper is a pure
  function in `web/app/(authed)/settings/` with vitest coverage
  (at minimum: OIDC-human-yes → false, credential-bearer-yes →
  true, empty-state → false, partial-empty → false).
- **AC-5.** Does NOT change the domain audit-log invariant (slice
  134's `audit_log` table shape). Verified by `git diff` not
  touching `internal/audit/` or `migrations/`.
- **AC-6.** Does NOT change the `/v1/me` wire shape if option 1 or
  2 chosen (a pure frontend fix); if option 3 chosen, the wire
  shape extension is documented in the decisions log AND adds the
  field as optional (backward-compatible).
- **AC-7.** Playwright e2e: a new spec exercises the credential-
  bearer path with the test fixture's `TEST_BEARER` (which is an
  admin_credential JWT per slice 197) and asserts the banner
  presence + degraded affordance copy.
- **AC-8.** Documentation: brief note in
  `docs/operators/settings.md` (if it exists) or
  `docs/audit-log/250-*.md` explaining when an operator should
  expect the credential-bearer banner.

## Constitutional invariants honored

- **AI-assist boundary (hard).** Banner copy is templated +
  deterministic; no LLM involvement.
- **Slice 103 (settings is user-facing-only).** Reaffirmed — this
  fix makes the user-vs-credential distinction explicit, which is
  the page's premise.
- **Slice 197 (final bearer retirement).** The
  credential-as-JWT path (slice 209) is the post-197 way
  credentials authenticate; this finding is downstream of that.

## Canvas references

- `Plans/canvas/04-evidence-engine.md` §4.6 — credentials vs human
  users in the evidence push path; same distinction applies to
  `/settings` audience.
- `Plans/mockups/settings.html` lines 136-167 — Profile section
  mockup explicitly an OIDC-human design.

## Dependencies

- **#204** (this slice's parent — per-page UI parity audit fleet).
- **#197** (final bearer retirement, credential-bearer JWT path) —
  merged.
- **#209** (local-credential-as-JWT) — composes; the credential-
  bearer JWT signature this slice detects is the JWT slice 209
  produces.
- **#154** (settings-only audit) — reference; F7's avatar/hero
  block stays as-is for the OIDC-human case.

## Anti-criteria (P0 — block merge)

- **P0-250-1.** Does NOT fabricate fields that don't exist on
  `/v1/me` (e.g. inventing an email from the credential's name).
- **P0-250-2.** Does NOT change the server-side authz gate.
- **P0-250-3.** Does NOT remove the OIDC-human-user Profile
  section affordances — only adds the credential-bearer branch.
- **P0-250-4.** Does NOT debug the v1.14.0 500-error class
  (P0-A4 of slice 204).
- **P0-250-5.** Does NOT regress the 11/11 settings.spec.ts ACs
  from slice 171 close-out.

## Skill mix (3-4)

1. React conditional rendering + helper extraction
2. Vitest pure-function coverage
3. Playwright credential-bearer fixture assertion
4. (Optional, if option 3) Go `/v1/me` wire-shape extension +
   sqlc/migration
