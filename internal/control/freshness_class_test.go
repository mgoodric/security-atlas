package control

import (
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/eval"
)

func freshnessManifest(class string) Manifest {
	return Manifest{
		BundleSchemaVersion: "1",
		BundleID:            "freshness_control",
		Title:               "Freshness control",
		SCFAnchorID:         "IAC-37",
		ImplementationType:  "automated",
		FreshnessClass:      class,
	}
}

// Every class the engine has a window for is a class a bundle may declare.
func TestValidateStructural_AcceptsEveryEngineFreshnessClass(t *testing.T) {
	t.Parallel()
	classes := eval.FreshnessClasses()
	if len(classes) == 0 {
		t.Fatal("eval.FreshnessClasses() is empty")
	}
	for _, c := range classes {
		m := freshnessManifest(c)
		if err := m.ValidateStructural(); err != nil {
			t.Errorf("freshness_class %q: engine maps it but the parser refused it: %v", c, err)
		}
	}
}

// A class the engine cannot map is refused, so a typo cannot silently widen
// a control's window to the 90-day fallback (#2896).
func TestValidateStructural_RefusesUnknownFreshnessClass(t *testing.T) {
	t.Parallel()
	// `hourly` was the one named exception until #2826 moved soc2_a1_1 to
	// `realtime`; it is refused like any other unmapped class.
	for _, c := range []string{"hourly", "hourley", "Daily", "biweekly", "minutely", " monthly"} {
		if _, known := eval.FreshnessMaxAge(c); known {
			t.Fatalf("test input %q is mapped by the engine; pick another typo", c)
		}
		m := freshnessManifest(c)
		err := m.ValidateStructural()
		if err == nil {
			t.Errorf("freshness_class %q: expected refusal, got nil", c)
			continue
		}
		if !strings.Contains(err.Error(), "freshness_class") {
			t.Errorf("freshness_class %q: error must name the field; got %v", c, err)
		}
	}
}
