- **demoseed:** slice 205 — comprehensive demo seed dataset.
  Adds `atlas-cli demo seed --tenant-slug=<slug>` (and a sibling
  `atlas-cli demo teardown`) that creates ONE polished demo tenant
  populated with ~50 controls, ~20 risks, ~200 evidence records spread
  across the prior 12 months, 5 policies, 3 framework scopes (SOC 2 /
  ISO 27001 / NIST CSF), 3 audit periods (1 frozen + 2 open), 10
  vendors, 10 exceptions, 5 walkthroughs, 3 board reports, and ~50
  audit-log rows. Every audit-log row carries
  `payload_json -> demo_seed_v = "205"` for forensic filtering. The
  command is opt-in via `ATLAS_ENABLE_DEMO_SEED=true`, generates a
  fresh one-time password per invocation (>=16 chars, mixed alphabet,
  no ambiguous glyphs), prints it once to stdout, and is idempotent on
  `--tenant-slug`. Refuses to write into any tenant already populated
  past the 10-row safety guard. New Go package `internal/demoseed/`,
  new migration `_020000_users_demo_only_flag.sql` adding
  `users.demo_only BOOLEAN NOT NULL DEFAULT FALSE` (load-bearing for
  a future slice that gates super_admin promotion against demo users),
  and a new doc at `docs/getting-started/demo-seed.md`.
