package scfseed_test

// SCF pin guard (issue #1717). The sample catalog at
// migrations/fixtures/scf-sample.json claims to be SCF, so every sample
// scf_id must name the real SCF control with that ID, title verbatim, in the
// pinned release recorded in testdata/scf-pin.json.
//
// knownMismatch is a ratchet. It lists the sample rows that do NOT match the
// pin today; the per-family follow-ups regenerate those rows (and re-anchor
// the stock controls, crosswalks and connectors that cite them) and delete
// the entries. A new mismatch fails the guard, and so does a stale entry
// that now matches. Never add an entry: fix the sample row instead.
//
// The concept -> 2026.3 remap the follow-ups apply is in
// docs/audit-log/1717-scf-pin-ratchet-decisions.md.
//
// testdata/scf-pin.json carries SCF IDs and titles only, never descriptions
// (OQ #1: the project does not bundle SCF content). It is regenerated from
// the published workbook with:
//
//	SCF_WORKBOOK=/path/to/secure-controls-framework-scf-2026-3.xlsx \
//	  go test -run TestRegenerateSCFPin ./internal/api/scfseed/
//
// The regeneration refuses a workbook whose sha256 differs from the pinned
// one; moving to a new release means changing scfPinRelease and
// scfPinWorkbookSHA256 here in the same PR.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

const (
	scfPinRelease        = "2026.3"
	scfPinWorkbookSHA256 = "5a89bf2d3c106a9a87d4b6e3d62dd3e147d0e960d4c07473045a10aa8a7df697"
	scfPinSourceURL      = "https://github.com/securecontrolsframework/securecontrolsframework/blob/main/secure-controls-framework-scf-2026-3.xlsx"
	scfPinPath           = "testdata/scf-pin.json"
	scfSamplePath        = "../../../migrations/fixtures/scf-sample.json"
)

// knownMismatchCeiling caps knownMismatch at its seeded size. Lower it as
// family groups land; never raise it.
const knownMismatchCeiling = 73

// knownMismatch: sample scf_id -> the sample title that does not match the
// pin. Seeded 2026-11-13 with every mismatching row; drained by family.
var knownMismatch = map[string]string{
	"AAA-01": "Auditing & Accountability",
	"AAA-10": "Event Log Retention",
	"AAA-12": "Audit Log Review",
	"AST-03": "Asset Inventory",
	"AST-09": "Asset Disposal",
	"BCD-02": "Business Continuity Plan",
	"BCD-09": "Backups",
	"BCD-11": "Backup Testing",
	"CFG-02": "Secure Baseline Configurations",
	"CFG-04": "Configuration Change Control",
	"CHG-02": "Change Control Process",
	"CLD-01": "Cloud Infrastructure Governance",
	"CLD-09": "Cloud Service Provider Risk Management",
	"CPL-01": "Compliance Management",
	"CPL-03": "Internal Audit Function",
	"CRY-01": "Use of Cryptographic Controls",
	"CRY-04": "Encryption At Rest",
	"CRY-08": "Encryption In Transit",
	"CRY-09": "Cryptographic Key Management",
	"CRY-11": "Cryptographic Integrity Verification",
	"CRY-12": "Transmission Integrity",
	"DCH-01": "Data Classification & Handling",
	"DCH-03": "Data Retention",
	"DCH-05": "Data Integrity",
	"DCH-07": "Media Transportation & Custody",
	"END-04": "Endpoint Security",
	"END-07": "Malicious Code Protection",
	"GOV-01": "Information Security Governance",
	"GOV-02": "Publishing Security Documentation",
	"GOV-03": "Periodic Review & Update of Security Documentation",
	"GOV-04": "Information Security Roles & Responsibilities",
	"HRS-01": "Human Resources Security",
	"HRS-04": "Security Awareness Training",
	"HRS-07": "Personnel Sanctions",
	"HRS-09": "Personnel Termination Process",
	"HRS-12": "Rules of Behavior (Acceptable Use)",
	"IAC-06": "Multi-Factor Authentication (MFA)",

	"IAC-07": "User Provisioning & Lifecycle",
	"IAC-10": "Authenticator Management",
	"IAC-15": "Account Review",
	"IAC-21": "Privileged Account Management",
	"IAC-24": "Emergency Access (Break-Glass) Accounts",
	"IAC-25": "Session Termination",
	"IRO-04": "Incident Response Plan",
	"IRO-09": "Incident Reporting",
	"MNT-02": "Controlled Maintenance",
	"MON-01": "Continuous Monitoring",
	"MON-08": "Anomalous Behavior Detection",
	"NET-01": "Network Security",
	"NET-04": "Boundary Protection",
	"NET-06": "Network Segmentation",
	"NET-08": "Wireless Network Security",
	"PES-04": "Physical Access Control",
	"PRI-01": "Privacy Governance",
	"PRI-04": "Data Subject Rights",
	"RSK-01": "Risk Management Program",
	"RSK-04": "Risk Assessment",
	"SEA-01": "Secure Engineering Principles",
	"SEA-05": "Secure Software Development",
	"TDA-01": "Technology Development & Acquisition",
	"THR-01": "Threat Intelligence Program",
	"THR-02": "Indicators of Exposure (IOE)",
	"THR-03": "Threat Intelligence Feeds",
	"THR-04": "Threat Hunting",
	"THR-05": "Insider Threat Program",
	"THR-06": "Insider Threat Awareness",
	"THR-07": "Vulnerability Disclosure Program (VDP)",
	"THR-09": "Threat Catalog",
	"THR-10": "Threat Analysis",
	"TPM-01": "Third-Party Management",
	"TPM-04": "Third-Party Risk Assessments",
	"TPM-05": "Third-Party Contract Requirements",
	"VPM-04": "Vulnerability Remediation Process",
}

type scfPinControl struct {
	SCFID string `json:"scf_id"`
	Title string `json:"title"`
}

type scfPin struct {
	Comment        string          `json:"$comment"`
	Attribution    string          `json:"attribution"`
	License        string          `json:"license"`
	ReleaseVersion string          `json:"release_version"`
	SourceURL      string          `json:"source_url"`
	WorkbookSHA256 string          `json:"workbook_sha256"`
	WorkbookSheet  string          `json:"workbook_sheet"`
	Controls       []scfPinControl `json:"controls"`
}

type scfSample struct {
	Controls []scfPinControl `json:"controls"`
}

func readJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
}

// pinTitles validates the pin's header against the constants above and
// returns scf_id -> title.
func pinTitles(t *testing.T) map[string]string {
	t.Helper()
	var pin scfPin
	readJSON(t, scfPinPath, &pin)
	if pin.ReleaseVersion != scfPinRelease || pin.WorkbookSHA256 != scfPinWorkbookSHA256 {
		t.Fatalf("%s pins SCF %s (sha256 %s); this guard expects %s (sha256 %s). Regenerate the pin.",
			scfPinPath, pin.ReleaseVersion, pin.WorkbookSHA256, scfPinRelease, scfPinWorkbookSHA256)
	}
	titles := make(map[string]string, len(pin.Controls))
	for _, c := range pin.Controls {
		if _, dup := titles[c.SCFID]; dup {
			t.Fatalf("%s: duplicate scf_id %s", scfPinPath, c.SCFID)
		}
		titles[c.SCFID] = c.Title
	}
	if len(titles) < 1000 {
		t.Fatalf("%s holds %d controls; a full SCF release has well over 1000", scfPinPath, len(titles))
	}
	return titles
}

// mismatches compares the sample against the pin: scf_id -> why it fails.
func mismatches(sample []scfPinControl, pin map[string]string) map[string]string {
	out := map[string]string{}
	for _, c := range sample {
		real, ok := pin[c.SCFID]
		switch {
		case !ok:
			out[c.SCFID] = fmt.Sprintf("%q: no such control in SCF %s", c.Title, scfPinRelease)
		case real != c.Title:
			out[c.SCFID] = fmt.Sprintf("%q: SCF %s titles it %q", c.Title, scfPinRelease, real)
		}
	}
	return out
}

// duplicateIDs returns every scf_id the sample lists more than once. A
// duplicate is never waivable: one SCF ID is one control.
func duplicateIDs(sample []scfPinControl) []string {
	seen := map[string]int{}
	for _, c := range sample {
		seen[c.SCFID]++
	}
	var out []string
	for id, n := range seen {
		if n > 1 {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

func TestSCFSampleMatchesPinnedRelease(t *testing.T) {
	t.Parallel()
	pin := pinTitles(t)
	var sample scfSample
	readJSON(t, scfSamplePath, &sample)

	sampleTitle := map[string]string{}
	for _, c := range sample.Controls {
		sampleTitle[c.SCFID] = c.Title
	}
	got := mismatches(sample.Controls, pin)

	var fails []string
	if len(knownMismatch) > knownMismatchCeiling {
		fails = append(fails, fmt.Sprintf("knownMismatch holds %d entries; the ceiling is %d and only ever comes down", len(knownMismatch), knownMismatchCeiling))
	}
	for _, id := range duplicateIDs(sample.Controls) {
		fails = append(fails, fmt.Sprintf("%s: duplicate scf_id in the sample", id))
	}
	for id, why := range got {
		if _, waived := knownMismatch[id]; !waived {
			fails = append(fails, fmt.Sprintf("%s %s", id, why))
		}
	}
	for id, title := range knownMismatch {
		cur, inSample := sampleTitle[id]
		switch {
		case !inSample:
			fails = append(fails, fmt.Sprintf("%s: stale knownMismatch entry, no longer in the sample; delete it", id))
		case cur != title:
			fails = append(fails, fmt.Sprintf("%s: sample title changed to %q but knownMismatch still waives %q; fix the row against the pin and delete the entry", id, cur, title))
		case got[id] == "":
			fails = append(fails, fmt.Sprintf("%s: now matches SCF %s; delete its knownMismatch entry", id, scfPinRelease))
		}
	}
	sort.Strings(fails)
	if len(fails) > 0 {
		t.Fatalf("scf-sample.json vs SCF %s pin:\n  %s", scfPinRelease, strings.Join(fails, "\n  "))
	}
}

func TestDuplicateIDs(t *testing.T) {
	t.Parallel()
	got := duplicateIDs([]scfPinControl{
		{SCFID: "TPM-10", Title: "a"}, {SCFID: "CRY-07", Title: "b"}, {SCFID: "TPM-10", Title: "c"},
	})
	if len(got) != 1 || got[0] != "TPM-10" {
		t.Fatalf("duplicateIDs = %v; want [TPM-10]", got)
	}
}

func TestMismatchesDetectsWrongTitleAndMissingID(t *testing.T) {
	t.Parallel()
	pin := map[string]string{"CRY-07": "Encrypting Data At Rest", "CRY-06": "Integrity of Data In Transit"}
	got := mismatches([]scfPinControl{
		{SCFID: "CRY-07", Title: "Encrypting Data At Rest"},
		{SCFID: "CRY-06", Title: "Encryption At Rest"},
		{SCFID: "AAA-01", Title: "Auditing & Accountability"},
	}, pin)
	if len(got) != 2 || got["CRY-06"] == "" || got["AAA-01"] == "" || got["CRY-07"] != "" {
		t.Fatalf("mismatches = %v; want CRY-06 (wrong title) and AAA-01 (missing) only", got)
	}
}

// TestRegenerateSCFPin rewrites testdata/scf-pin.json from the published
// workbook. It runs only when SCF_WORKBOOK is set.
func TestRegenerateSCFPin(t *testing.T) {
	path := os.Getenv("SCF_WORKBOOK")
	if path == "" {
		t.Skip("SCF_WORKBOOK unset; set it to the pinned SCF workbook to regenerate " + scfPinPath)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if got := hex.EncodeToString(sum[:]); got != scfPinWorkbookSHA256 {
		t.Fatalf("workbook sha256 %s; pinned %s. Moving releases means updating scfPinRelease and scfPinWorkbookSHA256 first.", got, scfPinWorkbookSHA256)
	}
	f, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	sheet := "SCF " + scfPinRelease
	rows, err := f.GetRows(sheet)
	if err != nil {
		t.Fatalf("sheet %q: %v", sheet, err)
	}
	idCol, titleCol := -1, -1
	for i, h := range rows[0] {
		switch strings.TrimSpace(h) {
		case "SCF #":
			idCol = i
		case "SCF Control":
			titleCol = i
		}
	}
	if idCol < 0 || titleCol < 0 {
		t.Fatalf("sheet %q: no SCF # / SCF Control header in %q", sheet, rows[0])
	}
	var controls []scfPinControl
	for _, r := range rows[1:] {
		if len(r) <= idCol || len(r) <= titleCol || strings.TrimSpace(r[idCol]) == "" {
			continue
		}
		controls = append(controls, scfPinControl{SCFID: strings.TrimSpace(r[idCol]), Title: strings.TrimSpace(r[titleCol])})
	}
	pin := scfPin{
		Comment: "SCF control IDs and titles, verbatim and unmodified, extracted from the pinned SCF workbook for the " +
			"scf-sample.json guard (issue #1717). IDs and titles only: descriptions and every other column are omitted " +
			"(OQ #1: the project does not bundle SCF content). Regenerate with TestRegenerateSCFPin; never hand-edit.",
		Attribution:    "Secure Controls Framework (SCF) " + scfPinRelease + ", (c) Secure Controls Framework Council, LLC. https://securecontrolsframework.com",
		License:        "CC BY-ND 4.0, https://creativecommons.org/licenses/by-nd/4.0/ (SCF EULA: https://securecontrolsframework.com/terms-conditions/)",
		ReleaseVersion: scfPinRelease,
		SourceURL:      scfPinSourceURL,
		WorkbookSHA256: scfPinWorkbookSHA256,
		WorkbookSheet:  sheet,
		Controls:       controls,
	}
	b, err := json.MarshalIndent(pin, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(scfPinPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(scfPinPath, append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s: %d controls from %s", scfPinPath, len(controls), sheet)
}
