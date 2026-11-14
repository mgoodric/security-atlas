//go:build integration

// Issue #1909: an image upgrade that rewords a stock SOC 2 control's
// description must keep the control, so its owner and evidence stay
// attached. The self-host bootstrap re-uploads every controls/soc2/ bundle
// on each boot (deploy/docker/bootstrap/bootstrap.sh, Phase 5b); this test
// plays that boot against an install whose controls carry the previous
// release's wording.

package control_test

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/mgoodric/security-atlas/internal/control"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// withDescription returns rawYAML with its top-level description replaced
// by desc, leaving every other key as it was.
func withDescription(t *testing.T, rawYAML []byte, desc string) []byte {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal(rawYAML, &doc); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	root := doc.Content[0]
	value := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: desc}
	replaced := false
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "description" {
			root.Content[i+1] = value
			replaced = true
		}
	}
	if !replaced {
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "description"}, value)
	}
	out, err := yaml.Marshal(&doc)
	if err != nil {
		t.Fatalf("encode manifest: %v", err)
	}
	return out
}

func TestStockSOC2Upgrade_RewordedDescriptionKeepsOwnerAndEvidence(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)

	tenant := dbtest.SeedTenant(t, admin,
		"control_manifest_revisions", "risk_control_links", "risks", "exceptions",
		"control_owner_assignments", "evidence_records", "controls",
	)
	store := control.NewStore(app)
	ctx, err := tenancy.WithTenant(context.Background(), tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	want := map[string]int{"owner": 1, "evidence": 1, "exception": 1, "risk_link": 1}

	soc2Dir := filepath.Join(repoRoot(t), "controls", "soc2")
	upgraded := 0
	forEachSOC2Bundle(t, func(name string, stock *control.Bundle) {
		anchor := stock.Manifest.SCFAnchorID
		seedSCFAnchor(t, admin, anchor, strings.SplitN(anchor, "-", 2)[0])

		// The previous release: same manifest, other wording.
		raw, err := os.ReadFile(filepath.Join(soc2Dir, name, "control.yaml"))
		if err != nil {
			t.Fatalf("%s: read manifest: %v", name, err)
		}
		prior, err := control.FinalizeBundleForHTTP(withDescription(t, raw, "Previous release wording for "+name+"."))
		if err != nil {
			t.Fatalf("%s: parse prior manifest: %v", name, err)
		}
		before, err := store.Upload(ctx, prior, "bootstrap")
		if err != nil {
			t.Fatalf("%s: prior upload: %v", name, err)
		}
		seedControlLinks(t, admin, tenant, before.ControlID)

		// The upgraded boot re-uploads the shipped bundle.
		after, err := store.Upload(ctx, stock, "bootstrap")
		if err != nil {
			t.Fatalf("%s: upgrade upload: %v", name, err)
		}
		if !after.UpdatedInPlace || after.ControlID != before.ControlID || after.Version != before.Version {
			t.Errorf("%s: upgrade should update the description in place; got %+v (prior id %s)", name, after, before.ControlID)
			return
		}
		if got := linkCounts(t, admin, tenant, before.ControlID); !maps.Equal(got, want) {
			t.Errorf("%s: links after upgrade: got %v, want %v", name, got, want)
		}
		upgraded++
	})
	if upgraded == 0 {
		t.Fatal("no stock SOC 2 bundle was upgraded; the test would pass vacuously")
	}
}
