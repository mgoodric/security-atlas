package frameworkscopes

import (
	"testing"

	"github.com/mgoodric/security-atlas/internal/frameworkscope"
)

func TestNeutraliseFormula(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"":             "",
		"plain":        "plain",
		"=SUM(A1)":     "'=SUM(A1)",
		"+1":           "'+1",
		"-1":           "'-1",
		"@cmd":         "'@cmd",
		"\tx":          "'\tx",
		"https://x/=y": "https://x/=y",
	} {
		if got := neutraliseFormula(in); got != want {
			t.Errorf("neutraliseFormula(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSoAFilename(t *testing.T) {
	t.Parallel()
	iso := frameworkscope.SoAExport{FrameworkSlug: "iso27001", FrameworkVersion: "2022"}
	if got := soaFilename(iso, "xlsx"); got != "statement-of-applicability-iso27001-2022.xlsx" {
		t.Errorf("iso filename = %q", got)
	}
	other := frameworkscope.SoAExport{FrameworkSlug: "nist_csf", FrameworkVersion: "2.0 \"final\""}
	if got := soaFilename(other, "csv"); got != "applicability-nist-csf-2-0-final.csv" {
		t.Errorf("other filename = %q", got)
	}
}
