package demoseed

import (
	"encoding/json"
	"os"
	"testing"
)

// demoSCFPinPath is the verbatim SCF ID + title extract the #1717 guard
// reads. DemoSCFRelease is the sample catalog's label for the release it
// was extracted from: "test-" + that release (#1773, #1774).
const demoSCFPinPath = "../api/scfseed/testdata/scf-pin.json"

func loadDemoSCFPin(t *testing.T) (release string, titles map[string]string) {
	t.Helper()
	raw, err := os.ReadFile(demoSCFPinPath)
	if err != nil {
		t.Fatalf("read %s: %v", demoSCFPinPath, err)
	}
	var pin struct {
		ReleaseVersion string `json:"release_version"`
		Controls       []struct {
			SCFID string `json:"scf_id"`
			Title string `json:"title"`
		} `json:"controls"`
	}
	if err := json.Unmarshal(raw, &pin); err != nil {
		t.Fatalf("parse %s: %v", demoSCFPinPath, err)
	}
	titles = make(map[string]string, len(pin.Controls))
	for _, c := range pin.Controls {
		titles[c.SCFID] = c.Title
	}
	return pin.ReleaseVersion, titles
}

// TestDemoSCFAnchors_MatchPinnedRelease asserts every demoSCFAnchors
// (code, title) pair is the pinned release's (code, title), so a demo
// anchor can never bind to a control that means something else when the
// seed adopts the loaded catalog (#2302).
func TestDemoSCFAnchors_MatchPinnedRelease(t *testing.T) {
	release, titles := loadDemoSCFPin(t)
	if "test-"+release != DemoSCFRelease {
		t.Fatalf("pin is SCF %s; DemoSCFRelease is %s, want test-%s", release, DemoSCFRelease, release)
	}
	for i, a := range demoSCFAnchors {
		want, ok := titles[a.SCFID]
		if !ok {
			t.Errorf("demoSCFAnchors[%d] %s is not in SCF %s", i, a.SCFID, release)
			continue
		}
		if a.Title != want {
			t.Errorf("demoSCFAnchors[%d] %s title %q; SCF %s title is %q", i, a.SCFID, a.Title, release, want)
		}
	}
}

// TestDemoQuestionnaireAnchors_InPinnedRelease asserts every anchored demo
// questionnaire question names a code in the pinned release.
func TestDemoQuestionnaireAnchors_InPinnedRelease(t *testing.T) {
	release, titles := loadDemoSCFPin(t)
	for _, q := range buildQuestionnaire("demo").Questions {
		if q.SCFAnchorID == nil {
			continue
		}
		if _, ok := titles[*q.SCFAnchorID]; !ok {
			t.Errorf("question %s anchor %s is not in SCF %s", q.Code, *q.SCFAnchorID, release)
		}
	}
}
