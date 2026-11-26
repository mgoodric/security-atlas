package oscal

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// verifyExportDoc is the auditor's independent-verification page. Its Step 1
// bash block recomputes the signed bundle digest with stock tools.
const verifyExportDoc = "../../docs-site/docs/audit/verify-export.md"

// recipeBlock returns the bash block of verify-export.md that rebuilds the
// signed digest (the one that echoes "recomputed:").
func recipeBlock(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(verifyExportDoc)
	if err != nil {
		t.Fatalf("read %s: %v", verifyExportDoc, err)
	}
	for _, m := range regexp.MustCompile("(?s)```bash\n(.*?)```").FindAllStringSubmatch(string(raw), -1) {
		if strings.Contains(m[1], `echo "recomputed:`) {
			return m[1]
		}
	}
	t.Fatalf("%s: no bash block echoing recomputed:", verifyExportDoc)
	return ""
}

// recipeTools are the stock tools the verify-export recipe runs.
var recipeTools = []string{"bash", "jq", "sha256sum"}

// requireRecipeTools skips when a recipe tool is missing, except in CI,
// where a missing tool fails the test: a CI runner that silently skipped
// would leave the published recipe unchecked (#3053).
func requireRecipeTools(t testing.TB, inCI bool) {
	t.Helper()
	for _, tool := range recipeTools {
		if _, err := exec.LookPath(tool); err != nil {
			if inCI {
				t.Fatalf("%s not on PATH in CI: %v", tool, err)
			}
			t.Skipf("%s not on PATH: %v", tool, err)
		}
	}
}

// #1742 review B3: the published stock-tools recipe must recompute exactly
// the digest bundleDigest signs, for a bundle with a freeze revision (every
// export since #1742) and for one without (exported before).
func TestVerifyExportRecipe(t *testing.T) {
	requireRecipeTools(t, os.Getenv("CI") != "")
	script := recipeBlock(t)
	members := []BundleMember{
		newMember("ssp.json", "system-security-plan", []byte(`{"ssp":1}`)),
		newMember("assessment-plan.json", "assessment-plan", []byte(`{"ap":1}`)),
		newMember("assessment-results.json", "assessment-results", []byte(`{"ar":1}`)),
		newMember("poam.json", "plan-of-action-and-milestones", []byte(`{"poam":1}`)),
	}
	cases := map[string]*Bundle{
		"revision 1":         {Members: members, FreezeRevision: 1, RevisionHash: strings.Repeat("ab", 32)},
		"amended revision 3": {Members: members, FreezeRevision: 3, RevisionHash: strings.Repeat("cd", 32), AmendReason: "late FY25 results"},
		"pre-#1742 bundle":   {Members: members},
	}
	for name, b := range cases {
		t.Run(name, func(t *testing.T) {
			digest := bundleDigest(b)
			b.Signature = Signature{Digest: hex.EncodeToString(digest[:])}
			man, err := json.Marshal(b.Manifest())
			if err != nil {
				t.Fatalf("marshal manifest: %v", err)
			}
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "manifest.json"), man, 0o600); err != nil {
				t.Fatalf("write manifest: %v", err)
			}
			cmd := exec.Command("bash", "-euo", "pipefail", "-c", script)
			cmd.Dir = dir
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("recipe: %v\n%s", err, out)
			}
			got := map[string]string{}
			for _, line := range strings.Split(string(out), "\n") {
				if k, v, ok := strings.Cut(line, ":"); ok {
					got[strings.TrimSpace(k)] = strings.TrimSpace(v)
				}
			}
			if got["recomputed"] == "" || got["recomputed"] != got["manifest"] {
				t.Fatalf("recipe recomputed %q, manifest digest %q\n%s", got["recomputed"], got["manifest"], out)
			}
		})
	}
}

// recordTB captures whether requireRecipeTools skipped or failed.
type recordTB struct {
	testing.TB
	failed, skipped bool
}

func (r *recordTB) Helper() {}

func (r *recordTB) Fatalf(string, ...any) {
	r.failed = true
	runtime.Goexit()
}

func (r *recordTB) Skipf(string, ...any) {
	r.skipped = true
	runtime.Goexit()
}

// #3053 N-G: with a recipe tool missing, the check fails in CI and skips
// elsewhere.
func TestRequireRecipeTools_FailsInCIWhenToolMissing(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for _, tc := range []struct {
		inCI               bool
		wantFail, wantSkip bool
	}{{true, true, false}, {false, false, true}} {
		r := &recordTB{TB: t}
		done := make(chan struct{})
		go func() {
			defer close(done)
			requireRecipeTools(r, tc.inCI)
		}()
		<-done
		if r.failed != tc.wantFail || r.skipped != tc.wantSkip {
			t.Errorf("inCI=%v: failed=%v skipped=%v, want failed=%v skipped=%v",
				tc.inCI, r.failed, r.skipped, tc.wantFail, tc.wantSkip)
		}
	}
}
