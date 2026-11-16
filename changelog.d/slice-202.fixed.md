- **self-host:** slice 202 — bundled mode atlas/atlas-bootstrap race.
  The atlas server's boot-time schema importer
  (`cmd/atlas/main.go::ImportPlatformSchemas`) runs ONCE without
  retry; if atlas started before atlas-bootstrap's phase-2
  migrations created `evidence_kind_schemas`, the importer failed
  with `relation "evidence_kind_schemas" does not exist` and the
  registry stayed empty, causing phase-6 control-bundle uploads to
  400 with `evidence_kind ... is not registered`. The smoke-test
  harness (`deploy/docker/test-self-host-bundle.sh`) now stages the
  bring-up: postgres + atlas-bootstrap come up first; the harness
  polls for the sentinel relation `evidence_kind_schemas` to exist
  (deterministic signal of phase-2 migrations complete, not a
  sleep); then atlas + web come up against a fully-migrated DB.
  Production compose unchanged. Both `bundled` and `external`
  matrix modes use the staged shape.
