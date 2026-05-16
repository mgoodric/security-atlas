# Evidence Schema Registry — Seed, Load, and Validate

_2026-05-16T06:15:21Z by Showboat 0.6.1_

<!-- showboat-id: 5184e643-3ead-4087-9ea7-4c63c8ec2bf7 -->

> **Walkthrough kind:** this is a PAI Walkthrough skill document (slice 070 — showboat-generated). It is distinct from slice 027's audit walkthrough (`internal/audit/walkthrough`), which records auditor evidence capture against controls. The two concepts share a word and nothing else.

## Overview

Every evidence record pushed into security-atlas declares an `evidence_kind` + `schema_version`. The platform refuses any record whose (kind, semver) is unknown to the schema registry, and refuses any payload that does not match that schema. This is the contract that lets the eval engine assume payload shapes (slice 014 + slice 068).

This walkthrough traces the three moments of that contract: **seed at boot** (`DefaultSeed()`), **load from DB** (`LoadFromDB()`), and **validate at ingest** (`ValidatePayload()`). Every block was captured by `uvx showboat exec` against the slice-037 docker-compose self-host bundle, seeded by `fixtures/walkthroughs/00-seed.sql` + `schema-registry.sql`.

## 1. DefaultSeed — The Bootstrapped Kind List

When the platform server starts and no \`SchemaRegistry\` is injected, the API config falls back to \`schemaregistry.DefaultSeed()\`. This is the canonical list of platform-shipped \`evidence_kind\` names — every one a \`.vN\`-suffixed identifier per the slice 014 + slice 068 contract.

```bash
cd /Users/gmoney/Development/security-atlas-070 && sed -n "86,108p" internal/api/schemaregistry/registry.go
```

```output
func DefaultSeed() []KindVersion {
	return []KindVersion{
		{Kind: "sast.scan_result.v1", Version: "1.0.0"},
		{Kind: "access_review.completion.v1", Version: "1.0.0"},
		{Kind: "manual.attestation.v1", Version: "1.0.0"},
		{Kind: "aws.s3.bucket_encryption_state.v1", Version: "1.0.0"},
		{Kind: "github.repo_protection.v1", Version: "1.0.0"},
		{Kind: "github.audit_event.v1", Version: "1.0.0"},
		{Kind: "github.scim_user.v1", Version: "1.0.0"},
		{Kind: "okta.mfa_policy.v1", Version: "1.0.0"},
		{Kind: "okta.app_assignment.v1", Version: "1.0.0"},
		{Kind: "okta.user_lifecycle.v1", Version: "1.0.0"},
		{Kind: "1password.org_policy.v1", Version: "1.0.0"},
		{Kind: "osquery.host_posture.v1", Version: "1.0.0"},
		{Kind: "jira.ticket_evidence.v1", Version: "1.0.0"},
		{Kind: "manual.upload.v1", Version: "1.0.0"},
		// Slice 023: policy acknowledgment workflow. Each
		// POST /v1/policies/{id}/acknowledge emits one record of this
		// kind through the slice-013 evidence ledger.
		{Kind: "policy.acknowledgment.v1", Version: "1.0.0"},
	}
}

```

Every kind ends in `.vN` (`aws.s3.bucket_encryption_state.v1`, `okta.mfa_policy.v1`). Bare-name kinds — `aws.s3.bucket_encryption_state` without the suffix — are a slice-068 regression and now have a drift-guard test in `internal/control` that fails the build if a control bundle reintroduces one.

## 2. The Embedded JSON Schemas

Each entry in `DefaultSeed` has a corresponding JSON Schema document under `internal/api/schemaregistry/schemas/<kind>/<version>.json`, embedded into the binary via `go:embed`. Let us see the embed wiring + the directory layout:

```bash
cd /Users/gmoney/Development/security-atlas-070 && sed -n "1,25p" internal/api/schemaregistry/embed.go
```

```output
package schemaregistry

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

// PlatformSchema is one bundled (kind, semver) pair plus its JSON Schema
// body. Slice 014 ships ten of these; the LoadPlatformSchemas helper
// walks an fs.FS rooted at schemas/ to find them. Keeping the loader
// fs.FS-shaped (rather than os.DirFS) means tests can swap in an
// in-memory FS and the production code does not need to know where
// the files live on disk.
type PlatformSchema struct {
	Kind              string
	Semver            string
	SchemaJSON        []byte
	Owner             string
	DefaultSCFAnchors []string
}

```

```bash
cd /Users/gmoney/Development/security-atlas-070 && ls internal/api/schemaregistry/schemas/
```

```output
1password.org_policy
access_review.completion
aws.s3.bucket_encryption_state
github.audit_event
github.repo_protection
github.scim_user
jira.ticket_evidence
manual.attestation
manual.upload
okta.app_assignment
okta.mfa_policy
okta.user_lifecycle
osquery.host_posture
policy.acknowledgment
sast.scan_result
```

Fifteen subdirectories — one per `DefaultSeed` entry. Each holds a `1.0.0.json` file with the actual JSON Schema body. Slice 015 also added an `x-redaction-rules` extension recognized at load time.

## 3. The DB Row Format

`DefaultSeed` is the in-memory bootstrap; the durable representation lives in `evidence_kind_schemas`. Each row carries the `(kind, semver, schema_json)` triple plus an `owner` (`platform` for shipped kinds, a tenant-specific owner for tenant-private kinds), `default_scf_anchors` (which SCF anchors a record of this kind hits by default), and timestamps.

```bash
docker exec security-atlas-pg-030 psql -U postgres -d security_atlas -c "SELECT kind, semver, owner, default_scf_anchors FROM evidence_kind_schemas WHERE tenant_id IS NULL ORDER BY kind;"
```

```output
           kind           | semver |  owner   | default_scf_anchors
--------------------------+--------+----------+---------------------
 demo.encryption_state.v1 | 1.0.0  | platform | {CRY-05}
(1 row)

```

(The walkthrough fixture seeds one synthetic kind — `demo.encryption_state.v1` — rather than the 15 production kinds. A fresh `just self-host-up` runs the platform `seed-stock` bootstrap step that installs all 15.)

## 4. LoadFromDB — The Slice-068 Retry Path

When the API server starts, it calls `LoadFromDB(ctx)` against the RLS app pool. Slice 068 added retry-with-backoff because in the docker-compose bundle the API server can race ahead of Postgres availability:

```bash
cd /Users/gmoney/Development/security-atlas-070 && sed -n "147,200p" internal/api/schemaregistry/service.go
```

```output
func (s *Service) LoadFromDB(ctx context.Context) error {
	q := dbx.New(s.pool)
	rows, err := q.ListEvidenceKindSchemasGlobal(ctx, dbx.ListEvidenceKindSchemasGlobalParams{
		Limit:  10000,
		Offset: 0,
	})
	if err != nil {
		return fmt.Errorf("list global schemas: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.compiled = map[string]*jsonschema.Schema{}
	s.byKey = map[string]storedSchema{}
	s.redaction = map[string][]string{}
	s.cache = New(nil)
	for _, r := range rows {
		compiled, err := compileSchema(r.SchemaJson)
		if err != nil {
			return fmt.Errorf("compile %s/%s: %w", r.Kind, r.Semver, err)
		}
		key := cacheKey(r.Kind, r.Semver)
		s.compiled[key] = compiled
		s.byKey[key] = storedSchema{
			id:                r.ID,
			kind:              r.Kind,
			semver:            r.Semver,
			schemaJSON:        r.SchemaJson,
			owner:             r.Owner,
			defaultSCFAnchors: r.DefaultScfAnchors,
		}
		// Slice 015: extract x-redaction-rules. A malformed list is
		// fatal at load — we'd rather refuse to start than silently
		// fail-open on secret redaction.
		rules, rerr := redact.ExtractRulesFromSchema(r.SchemaJson)
		if rerr != nil {
			return fmt.Errorf("redaction rules %s/%s: %w", r.Kind, r.Semver, rerr)
		}
		if len(rules) > 0 {
			s.redaction[key] = rules
		}
		s.cache.Register(r.Kind, r.Semver)
	}
	return nil
}

// IsRegisteredForTenant implements ingest.TenantAwareRegistry (slice 015).
// Returns true if (kind, semver) is registered globally OR as a private
// kind under tenantID. The global cache is hot; tenant-private kinds
// fall through to the lookupCompiled slow path which hits the DB once
// per (tenant, kind, semver) then memoizes.
//
// AC-6 / TestAC6_RedactionAtIngestion depends on this: the test
// registers `secret.scan.v1` as a tenant-private kind, so a
// global-only IsRegistered probe would reject the push as unknown
```

The load is straightforward: list all global rows (`tenant_id IS NULL`), compile each schema with the JSON Schema 2020-12 compiler, and stash it in two per-key maps (`compiled` for validation, `byKey` for metadata). Slice 015 extends it: `x-redaction-rules` in the schema body is extracted at load — a malformed list is fatal so the platform refuses to start rather than silently fail-open on secret redaction.

The slice-068 retry wrapper lives in `cmd/atlas/main.go`:

```bash
cd /Users/gmoney/Development/security-atlas-070 && grep -n -B1 -A 5 "LoadFromDB" cmd/atlas/main.go | head -25
```

```output
666-
667:// retrySchemaCacheLoad calls schemaSvc.LoadFromDB on the app pool, retrying
668-// with a fixed 2s backoff until it succeeds or `budget` elapses. The common
669-// transient failure is `password authentication failed for user
670-// "atlas_app"` (SQLSTATE 28P01) during the docker-compose self-host bundle's
671-// parallel atlas / atlas-bootstrap startup window, before bootstrap.sh has
672:// run `ALTER ROLE atlas_app PASSWORD ...`. Each LoadFromDB call gets its own
673-// 10s timeout. Returns nil on the first success, or the last error if the
674-// budget runs out (the caller logs it; boot continues, matching the prior
675-// non-fatal behaviour). Respects ctx cancellation so SIGTERM during boot is
676-// honoured.
677-func retrySchemaCacheLoad(ctx context.Context, schemaSvc *schemaregistry.Service, budget time.Duration) error {
--
682-		loadCtx, loadCancel := context.WithTimeout(ctx, 10*time.Second)
683:		err := schemaSvc.LoadFromDB(loadCtx)
684-		loadCancel()
685-		if err == nil {
686-			if attempt > 1 {
687-				fmt.Fprintf(os.Stderr, "atlas: schema cache loaded after %d attempt(s)\n", attempt)
688-			}
```

## 5. The Validation Path

When an evidence record arrives at the platform, the ingest path calls `ValidatePayload(kind, semver, payload)`. The compiled schema lives in `s.compiled` keyed by `cacheKey(kind, semver)`; validation is a synchronous JSON Schema check.

For the walkthrough we built one synthetic schema, `demo.encryption_state.v1`, expecting `{bucket: string, encrypted: boolean}` with `additionalProperties: false`. Let’s confirm a valid payload accepts:

```bash
docker exec security-atlas-pg-030 psql -U postgres -d security_atlas -c "SELECT jsonb_pretty(schema_json) FROM evidence_kind_schemas WHERE kind = 'demo.encryption_state.v1';" 2>&1 | head -25
```

```output
                          jsonb_pretty
----------------------------------------------------------------
 {                                                             +
     "$id": "demo.encryption_state.v1",                        +
     "type": "object",                                         +
     "$schema": "https://json-schema.org/draft/2020-12/schema",+
     "required": [                                             +
         "bucket",                                             +
         "encrypted"                                           +
     ],                                                        +
     "properties": {                                           +
         "bucket": {                                           +
             "type": "string",                                 +
             "minLength": 1                                    +
         },                                                    +
         "encrypted": {                                        +
             "type": "boolean"                                 +
         }                                                     +
     },                                                        +
     "x-evidence-kind": "demo.encryption_state.v1",            +
     "additionalProperties": false                             +
 }
(1 row)

```

## 6. Validate Live: Accept a Good Payload

Build a `jsonschema-cli` style validator against the schema row we just printed and run a good payload through it. The same library security-atlas uses internally (`github.com/santhosh-tekuri/jsonschema/v6`) is exposed via the `Service.ValidatePayload` method; we use a Go one-liner to call it head-on:

```bash
cd /Users/gmoney/Development/security-atlas-070 && go run /tmp/validate-good.go
```

```output
ACCEPTED: payload conforms to demo.encryption_state.v1
```

## 7. Reject a Bad Payload

Now break the contract — missing `encrypted` field — and confirm the validator rejects:

```bash
cd /Users/gmoney/Development/security-atlas-070 && go run /tmp/validate-bad.go 2>&1 | head -8
```

```output
REJECTED: jsonschema validation failed with 'file:///Users/gmoney/Development/security-atlas-070/demo.encryption_state.v1#'
- at '': missing property 'encrypted'
```

## 8. Unknown Kind Fast Path

What happens when a record claims a kind that the registry has never heard of? The ingest path short-circuits with `ErrUnknownKind` before ever reaching `Validate`:

```bash
cd /Users/gmoney/Development/security-atlas-070 && grep -B1 -A 5 "ErrUnknownKind" internal/api/schemaregistry/service.go | head -15
```

```output
	// Slow path: hydrate the tenant cache from the DB. lookupCompiled
	// returns ErrUnknownKind when the row is absent for both tenant
	// and global namespace, which we map to false.
	if _, err := s.lookupCompiled(ctx, tenantID, kind, version); err == nil {
		return true
	}
	return false
--
// over a global kind of the same (kind, semver). Returns nil on conform;
// ErrUnknownKind if the (kind, semver) is not registered; or a wrapped
// validation error.
func (s *Service) ValidatePayload(ctx context.Context, tenantID, kind, version string, payload []byte) error {
	compiled, err := s.lookupCompiled(ctx, tenantID, kind, version)
	if err != nil {
		return err
```

`lookupCompiled` returns `ErrUnknownKind` when no row exists for either the tenant or the global namespace, and the ingest path maps that to a 422 response (`"unregistered evidence_kind"`).

## 9. Putting It All Together

The schema registry contract is small and load-bearing. Boot-time:

1. **DefaultSeed** (section 1) lists the platform kinds — every name `.vN`-suffixed.
2. **Embedded schemas** (section 2) ship the JSON Schema bodies alongside the binary.
3. **DB row** (section 3) is the durable representation — `evidence_kind_schemas` with `tenant_id IS NULL` for platform-shipped rows.
4. **LoadFromDB** (section 4) hydrates the in-memory compiled cache; slice 068 added retry-with-backoff for docker-compose boot races.

Per request:

5. **Lookup** (section 8) hits the per-tenant cache, falls through to the global cache, raises `ErrUnknownKind` on miss.
6. **Validate** (sections 5-7) runs the JSON Schema compiler against the record payload; conform → ingest, fail → 422.

Tenant-private kinds (slice 015) ride the same paths with a tenant-scoped row. Slice 015 also extends the schema with `x-redaction-rules`, extracted at load — a malformed list refuses platform startup rather than silently fail-open on secret redaction.

### Where to read more

- **Canvas:** [`Plans/canvas/04-evidence-engine.md`](../../Plans/canvas/04-evidence-engine.md) §4 — Evidence SDK + schema registry, §4.5 — `.vN` versioning rule
- **Slice docs:** [`docs/issues/014-schema-registry.md`](../issues/014-schema-registry.md) (registry introduced), [`docs/issues/015-redaction-rules.md`](../issues/015-redaction-rules.md) (`x-redaction-rules` + tenant-private kinds), [`docs/issues/068-schema-registry-evidence-kind-fix.md`](../issues/068-schema-registry-evidence-kind-fix.md) (retry-with-backoff)
- **Go package:** [`internal/api/schemaregistry/`](../../internal/api/schemaregistry/) — `DefaultSeed`, `LoadFromDB`, `ValidatePayload`, `IsRegisteredForTenant`
