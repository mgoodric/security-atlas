//go:build integration

package soc2import_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

// Slice 641 — the FULL SCF Threat-Management (THR) domain is present in the
// seeded sample catalog.
//
// Slice 635 seeded a single domain-head anchor (THR-01 "Threat Intelligence
// Program") so the datadog.siem_rule.v1 advisory detection anchor would
// resolve. Slice 641 imports the rest of the canonical SCF THR domain
// (threat-intel feeds, threat hunting, insider-threat program, vulnerability
// disclosure, threat catalog/analysis, etc.) into the sample catalog, mirroring
// how other families carry their representative control set. This file is the
// load-bearing proof that every imported THR anchor resolves through the exact
// production path the crosswalk importer uses (GetSCFAnchorBySCFID against
// slug='scf' AND status='current').

// thrDomain is the full set of canonical SCF Threat-Management anchors the
// sample catalog carries after slice 641. THR-01 was seeded by slice 635 and
// reconciled here; the remainder are added by slice 641. The list is the
// assertion surface — if a maintainer adds or removes a THR anchor in the
// fixture, this list moves in lockstep with it.
var thrDomain = []string{
	"THR-01", // Threat Intelligence Program (reconciled — slice 635 head)
	"THR-02", // Indicators of Exposure (IOE)
	"THR-03", // Threat Intelligence Feeds
	"THR-04", // Threat Hunting
	"THR-05", // Insider Threat Program
	"THR-06", // Insider Threat Awareness
	"THR-07", // Vulnerability Disclosure Program (VDP)
	"THR-09", // Threat Catalog
	"THR-10", // Threat Analysis
}

// TestTHRDomain_AllAnchorsResolveInSeededCatalog asserts every THR-domain
// anchor slice 641 imports resolves in the seeded catalog with family THR.
// A missing row here is a fixture regression: a THR anchor was dropped or its
// scf_id/family was mistyped.
func TestTHRDomain_AllAnchorsResolveInSeededCatalog(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	resetCatalog(t, pool)
	ensureSCFLoaded(t, pool)

	q := dbx.New(pool)
	for _, scfID := range thrDomain {
		scfID := scfID
		t.Run(scfID, func(t *testing.T) {
			anchor, err := q.GetSCFAnchorBySCFID(context.Background(), scfID)
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					t.Fatalf("%s does not resolve in the seeded SCF catalog — slice 641 THR-domain regression (the anchor was dropped from migrations/fixtures/scf-sample.json)", scfID)
				}
				t.Fatalf("GetSCFAnchorBySCFID(%s): %v", scfID, err)
			}
			if anchor.ScfID != scfID {
				t.Fatalf("resolved scf_id = %q; want %q", anchor.ScfID, scfID)
			}
			if anchor.Family != "THR" {
				t.Fatalf("%s family = %q; want THR (the dedicated threat-management domain)", scfID, anchor.Family)
			}
			if anchor.Title == "" {
				t.Fatalf("%s has an empty title; the catalog fixture must carry verbatim SCF control text", scfID)
			}
		})
	}
}

// TestTHRDomain_HasMoreThanTheDomainHead is the slice-641 grain assertion:
// the catalog now carries the THR domain as a multi-control family, not just
// the single slice-635 domain-head anchor. It guards against a regression that
// silently collapses the import back to the THR-01-only state.
func TestTHRDomain_HasMoreThanTheDomainHead(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	resetCatalog(t, pool)
	ensureSCFLoaded(t, pool)

	var count int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM scf_anchors a
		JOIN framework_versions fv ON fv.id = a.framework_version_id
		JOIN frameworks f ON f.id = fv.framework_id
		WHERE f.slug = 'scf' AND fv.status = 'current' AND f.tenant_id IS NULL
		  AND a.family = 'THR'`).Scan(&count); err != nil {
		t.Fatalf("count THR anchors: %v", err)
	}
	if count != len(thrDomain) {
		t.Fatalf("seeded THR-domain anchor count = %d; want %d (the full slice-641 THR domain)", count, len(thrDomain))
	}
	if count <= 1 {
		t.Fatalf("THR domain collapsed to %d anchor(s); slice 641 imports the full domain (>1)", count)
	}
}
