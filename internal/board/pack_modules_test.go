package board

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// switchesOff is a Switches with every module on except those listed.
type switchesOff map[string]bool

func (s switchesOff) SwitchedOnStrict(_ context.Context, _ uuid.UUID, key string) (bool, error) {
	return !s[key], nil
}

// switchesDown fails every switch read, as a down flag store does.
type switchesDown struct{}

func (switchesDown) SwitchedOnStrict(context.Context, uuid.UUID, string) (bool, error) {
	return false, errors.New("flag store unavailable")
}

// #2548: a switch that cannot be read fails generation; the pack is never
// built from a guessed section set.
func TestPackGenerator_OmittedFailsClosedOnSwitchError(t *testing.T) {
	t.Parallel()
	ctx, err := tenancy.WithTenant(context.Background(), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	g := (&PackGenerator{}).WithSwitches(switchesDown{})
	if got, err := g.omitted(ctx); err == nil {
		t.Fatalf("omitted = %v, nil; want an error", got)
	}
}

// #2175: the sections whose module is switched off are omitted, in
// canonical order; with no switches every section is generated.
func TestPackGenerator_OmittedFollowsSwitches(t *testing.T) {
	t.Parallel()
	ctx, err := tenancy.WithTenant(context.Background(), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	g := &PackGenerator{}
	if got, err := g.omitted(ctx); err != nil || len(got) != 0 {
		t.Fatalf("no switches: omitted = %v, %v", got, err)
	}
	g.WithSwitches(switchesOff{"third_party_risk": true, "risk": true})
	got, err := g.omitted(ctx)
	if err != nil {
		t.Fatalf("omitted: %v", err)
	}
	if strings.Join(got, ",") != "top_risks,vendor_burndown" {
		t.Fatalf("omitted = %v; want top_risks, vendor_burndown", got)
	}
}

// A pack with omitted sections publishes once the sections it carries are
// approved, and its narrative leaves the omitted ones out.
func TestPack_OmittedSectionsLeaveGateAndNarrative(t *testing.T) {
	t.Parallel()
	p := Pack{PeriodEnd: "2026-09-30", Sections: map[string]Section{}, Omitted: []string{SectionVendorBurndown}}
	for _, key := range p.Keys() {
		sec := newSection(key, SectionData{})
		sec.Approved = true
		p.Sections[key] = sec
	}
	if title, ok := allSectionsApproved(p); !ok {
		t.Fatalf("allSectionsApproved = false at %q", title)
	}
	md, err := RenderPackNarrative(p)
	if err != nil {
		t.Fatalf("RenderPackNarrative: %v", err)
	}
	if strings.Contains(md, sectionTitles[SectionVendorBurndown]) {
		t.Error("narrative names the omitted vendor burndown section")
	}
	// A pack that lists nothing omitted still needs every section.
	p.Omitted = nil
	if _, ok := allSectionsApproved(p); ok {
		t.Error("a pack missing vendor_burndown without omitting it passed the gate")
	}
}
