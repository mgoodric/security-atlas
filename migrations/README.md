# migrations/

[Atlas](https://atlasgo.io) declarative migrations (`schema.hcl`) for the platform Postgres database.

Slice 002 lands the schema for the seven domain entities (Control, Risk, Evidence, Scope, Framework + FrameworkVersion, Policy, FrameworkScope) plus RLS policies and tenancy plumbing.

Empty in slice 001.
