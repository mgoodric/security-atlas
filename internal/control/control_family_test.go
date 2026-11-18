package control_test

import (
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/control"
)

// controlFamilyExceptions lists stock bundles whose control_family may
// differ from their anchor's SCF family. Each entry needs a comment saying
// why. It is empty: no stock control has a reason to differ today.
var controlFamilyExceptions = map[string]string{}

// A stock control's control_family is its anchor's SCF family, the prefix
// of scf_anchor_id (SAT-04 is family SAT). A remap that moves a control
// across families must move the family with it, or the label drifts from
// what the control is filed under (#2405: cc1_4 kept HRS after HRS-04 moved
// to SAT-04). A bundle with no anchor or no declared family is skipped: the
// store derives the family from the anchor row in that case.
func TestStockControlFamilyMatchesAnchorPrefix(t *testing.T) {
	t.Parallel()
	root := filepath.Join(repoRoot(t), "controls")
	parsed := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Name() != "control.yaml" {
			return nil
		}
		dir := filepath.Dir(path)
		b, perr := control.ParseDirectory(dir)
		if perr != nil {
			t.Fatalf("parse bundle %s: %v", dir, perr)
		}
		parsed++
		name := filepath.Base(dir)
		anchor, family := b.Manifest.SCFAnchorID, b.Manifest.ControlFamily
		if anchor == "" || family == "" {
			return nil
		}
		want, _, _ := strings.Cut(anchor, "-")
		_, excepted := controlFamilyExceptions[name]
		switch {
		case family != want && !excepted:
			t.Errorf("%s: control_family %q, but scf_anchor_id %s is family %q; set control_family: %s or add a commented entry to controlFamilyExceptions", name, family, anchor, want, want)
		case family == want && excepted:
			t.Errorf("%s: on controlFamilyExceptions but control_family %q already matches %s; remove the entry", name, family, anchor)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	if parsed == 0 {
		t.Fatal("no control bundles parsed under controls/")
	}
}
