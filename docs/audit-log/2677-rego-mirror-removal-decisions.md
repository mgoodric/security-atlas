# 2677 — Remove the `policies/authz/` Rego mirror: decisions log

Issue: mgoodric/security-atlas#2677. Follow-up to #2675 / #2410.

## Detection-tier classification

- **detection_tier_actual:** none. No bug surfaced; this removes a copy
  that had drifted once already (#2410).
- **detection_tier_target:** none.

## Decisions

### D1 — delete the mirror rather than add `just authz-sync`

Matt decided this on 2026-11-20 (issue comment). The runtime loads only
`//go:embed all:rego_bundle` (`internal/authz/decision.go`). The only
reader of `policies/authz/` was `TestRegoMirrorMatchesEmbeddedBundle`,
and no CI job, release artifact or self-hosting doc used the directory.
Both `policies/authz/*.rego` and `internal/authz/rego_mirror_2410_test.go`
are deleted. `internal/authz/rego_bundle/` is now the single copy.

### D2 — what was repointed

Live code comments in `internal/` and `web/lib/` that named
`policies/authz/` now name `internal/authz/rego_bundle/`. The two
`just authz-sync` mentions (`decision.go`, `reload_test.go`) are dropped;
the recipe never existed. `policies/README.md` (which still said "Empty
in slice 001") now says where the Rego lives, and
`docs/audit-log/authz-review.md` names the new directory and notes that
the slice-035 review read the files at the old path.

### D3 — what was left alone

- Historical decisions logs, `docs/audits/`, `CHANGELOG.md`,
  `changelog.d/` and `docs/issues/`: they record what was true at the
  time.
- `migrations/sql/20260511000018_rbac_authz.sql`. Its comment on the
  `user_roles_role_chk` CHECK (around line 52) still says to add a
  `.rego` file under `policies/authz/`. That comment is now stale. It is
  an applied migration; editing it risks a checksum mismatch, and the
  runner's policy on edited migrations was not confirmed. The correct
  location is `internal/authz/rego_bundle/`, as the `Role` type comment
  in `internal/authz/roles.go` now says.
