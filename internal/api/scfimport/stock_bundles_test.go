package scfimport

import (
	"os"
	"slices"
	"testing"
)

// The stock list must name exactly the bundles bootstrap.sh uploads from
// controls/soc2/, or a fresh install would be refused as holding real data.
func TestStockControlBundleIDs_MatchControlsDir(t *testing.T) {
	entries, err := os.ReadDir("../../../controls/soc2")
	if err != nil {
		t.Fatalf("read controls/soc2: %v", err)
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		}
	}
	got := slices.Sorted(slices.Values(StockControlBundleIDs))
	if !slices.Equal(got, dirs) {
		t.Errorf("StockControlBundleIDs drifted from controls/soc2/:\n got  %v\n want %v", got, dirs)
	}
}

func TestIsShippedStockBundle(t *testing.T) {
	t.Parallel()
	if !IsShippedStockBundle("soc2_cc8_1_change_management") {
		t.Error("soc2_cc8_1_change_management ships; IsShippedStockBundle says it does not")
	}
	if IsShippedStockBundle("soc2_retired_bundle") || IsShippedStockBundle("") {
		t.Error("IsShippedStockBundle accepts a bundle that does not ship")
	}
}
