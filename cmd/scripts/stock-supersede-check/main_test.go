package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const bundle = `bundle_id: b1
title: "T"
description: "prose"
freshness_class: monthly
evidence_queries:
  - id: q1
    language: rego
    expression: "allow"
    description: "query prose"
`

func swap(t *testing.T, s, old, new string) string {
	t.Helper()
	if !strings.Contains(s, old) {
		t.Fatalf("swap: %q not found", old)
	}
	return strings.Replace(s, old, new, 1)
}

func TestEvaluate(t *testing.T) {
	t.Parallel()
	superseding := swap(t, bundle, "monthly", "annual")
	cases := []struct {
		name       string
		next       string
		labels     []string
		dlogs      []string
		wantOK     bool
		wantFields string
	}{
		{"unchanged", bundle, nil, nil, true, ""},
		{"query description only", swap(t, bundle, `"query prose"`, `"reworded"`), nil, nil, true, ""},
		{"supersede, no label", superseding, nil, []string{"b1 upgrade note"}, false, "freshness_class"},
		{"supersede, label but no d-log", superseding, []string{AckLabel}, nil, false, "freshness_class"},
		{"supersede, label and d-log naming another bundle", superseding, []string{AckLabel}, []string{"b2"}, false, "freshness_class"},
		{"supersede, label and d-log", superseding, []string{"bug", AckLabel}, []string{"re-assign owners for b1"}, true, "freshness_class"},
		{"query expression", swap(t, bundle, `"allow"`, `"deny"`), nil, nil, false, "evidence_queries[q1].expression"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var dlogs [][]byte
			for _, d := range tc.dlogs {
				dlogs = append(dlogs, []byte(d))
			}
			res, err := evaluate(map[string][]byte{"b1": []byte(bundle)}, map[string][]byte{"b1": []byte(tc.next)}, tc.labels, dlogs)
			if err != nil {
				t.Fatal(err)
			}
			if res.OK() != tc.wantOK {
				t.Fatalf("OK = %v, want %v (%+v)", res.OK(), tc.wantOK, res)
			}
			var got string
			if len(res.Supersedes) == 1 {
				got = strings.Join(res.Supersedes[0].Fields, ",")
			}
			if got != tc.wantFields {
				t.Fatalf("fields = %q, want %q", got, tc.wantFields)
			}
		})
	}
}

func TestEvaluate_NewAndRemovedBundlesDoNotSupersede(t *testing.T) {
	t.Parallel()
	res, err := evaluate(
		map[string][]byte{"gone": []byte(bundle)},
		map[string][]byte{"fresh": []byte(swap(t, bundle, "b1", "fresh"))},
		nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Supersedes) != 0 {
		t.Fatalf("got %+v", res.Supersedes)
	}
}

// TestRun_GitRepo drives run() end to end against a throwaway repo, the
// way CI calls it.
func TestRun_GitRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	t.Chdir(dir)
	sh := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(p, s string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sh("init", "-q")
	write("controls/soc2/b1/control.yaml", bundle)
	sh("add", ".")
	sh("commit", "-q", "-m", "base")

	exec1 := func(labels string) (int, string) {
		var out, errb bytes.Buffer
		code := run([]string{"--base", "HEAD", "--labels", labels}, &out, &errb)
		return code, out.String() + errb.String()
	}

	write("controls/soc2/b1/control.yaml", swap(t, bundle, `"query prose"`, `"reworded"`))
	if code, out := exec1(""); code != 0 {
		t.Fatalf("query description edit: exit %d\n%s", code, out)
	}

	write("controls/soc2/b1/control.yaml", swap(t, bundle, "monthly", "annual"))
	code, out := exec1("")
	if code != 1 || !strings.Contains(out, "b1: freshness_class") {
		t.Fatalf("freshness change: exit %d\n%s", code, out)
	}

	write("docs/audit-log/9999-x.md", "b1 supersedes: re-assign owners and re-upload evidence.\n")
	if code, out := exec1(AckLabel); code != 0 {
		t.Fatalf("labelled and logged (untracked d-log): exit %d\n%s", code, out)
	}
	if code, out := exec1("other"); code != 1 {
		t.Fatalf("logged without label: exit %d\n%s", code, out)
	}

	if code, _ := exec1(""); code != 1 {
		t.Fatal("unlabelled run should fail")
	}
	var errb bytes.Buffer
	if code := run(nil, &errb, &errb); code != 2 {
		t.Fatalf("missing --base: exit %d", code)
	}
}
