// stock-supersede-check is the issue #2891 CI tripwire. Every start of an
// existing deployment re-uploads the stock control bundles
// (deploy/docker/bootstrap/bootstrap.sh), and an upload that supersedes a
// control mints a new control id: the old row keeps the owners, evidence,
// evaluations, exceptions and links (docs/audit-log/1908). So a PR that
// changes a stock bundle in a superseding way orphans tenant data on
// upgrade, and a reviewer should be told.
//
// It compares every control.yaml under --dir in the working tree with the
// one carrying the same bundle_id at --base, using
// control.SupersedingFields, the function Store.Upload uses. A bundle new
// in this PR, or removed by it, is not a supersede.
//
// # Passing with a supersede
//
// When a bundle would supersede, the check fails unless BOTH:
//
//   - the PR carries the `supersedes-stock` label (--labels, one per line,
//     as CI passes the PR's labels), and
//   - a docs/audit-log/*.md file changed since --base names the bundle_id,
//     so the decision and its upgrade note are on record.
//
// How to run
//
//	go run ./cmd/scripts/stock-supersede-check --base origin/main
//
// Exit codes
//
//	0 — nothing supersedes, or every supersede is labelled and logged
//	1 — an unacknowledged supersede (each bundle and field on stderr)
//	2 — usage, git or parse failure
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/mgoodric/security-atlas/internal/control"
)

// AckLabel is the PR label that acknowledges a stock supersede.
const AckLabel = "supersedes-stock"

// dlogDir is where the acknowledging decisions log must live.
const dlogDir = "docs/audit-log"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("stock-supersede-check", flag.ContinueOnError)
	fl.SetOutput(stderr)
	base := fl.String("base", "", "git revision to compare against (required)")
	dir := fl.String("dir", "controls", "directory of stock bundles, relative to the repo root")
	labels := fl.String("labels", "", "the PR's labels, one per line")
	if err := fl.Parse(args); err != nil {
		return 2
	}
	if *base == "" {
		sayln(stderr, "stock-supersede-check: --base is required")
		return 2
	}

	prior, err := baseManifests(*base, *dir)
	if err != nil {
		say(stderr, "stock-supersede-check: %v\n", err)
		return 2
	}
	next, err := treeManifests(*dir)
	if err != nil {
		say(stderr, "stock-supersede-check: %v\n", err)
		return 2
	}
	dlogs, err := changedDLogs(*base)
	if err != nil {
		say(stderr, "stock-supersede-check: %v\n", err)
		return 2
	}

	res, err := evaluate(prior, next, splitLines(*labels), dlogs)
	if err != nil {
		say(stderr, "stock-supersede-check: %v\n", err)
		return 2
	}
	return report(res, stdout, stderr)
}

// Supersede is one stock bundle a PR would supersede on upgrade.
type Supersede struct {
	BundleID string
	Fields   []string
	Logged   bool // a changed decisions log names the bundle
}

// Result is the outcome of comparing a PR's stock bundles with its base.
type Result struct {
	Supersedes []Supersede
	Labelled   bool
}

// OK reports whether the check passes.
func (r Result) OK() bool {
	if len(r.Supersedes) == 0 {
		return true
	}
	if !r.Labelled {
		return false
	}
	for _, s := range r.Supersedes {
		if !s.Logged {
			return false
		}
	}
	return true
}

// evaluate compares prior and next (bundle_id -> control.yaml bytes).
// dlogs holds the contents of every decisions log changed in the PR.
func evaluate(prior, next map[string][]byte, labels []string, dlogs [][]byte) (Result, error) {
	var res Result
	for _, l := range labels {
		if l == AckLabel {
			res.Labelled = true
		}
	}
	ids := make([]string, 0, len(next))
	for id := range next {
		if _, ok := prior[id]; ok {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		fields, err := control.SupersedingFields(prior[id], next[id])
		if err != nil {
			return Result{}, fmt.Errorf("%s: %w", id, err)
		}
		if len(fields) == 0 {
			continue
		}
		s := Supersede{BundleID: id, Fields: fields}
		for _, d := range dlogs {
			if bytes.Contains(d, []byte(id)) {
				s.Logged = true
				break
			}
		}
		res.Supersedes = append(res.Supersedes, s)
	}
	return res, nil
}

func report(res Result, stdout, stderr io.Writer) int {
	if len(res.Supersedes) == 0 {
		sayln(stdout, "stock-supersede-check: no stock bundle supersedes; every change updates in place")
		return 0
	}
	out := stderr
	if res.OK() {
		out = stdout
	}
	say(out, "stock-supersede-check: %d stock bundle(s) would SUPERSEDE on upgrade.\n", len(res.Supersedes))
	sayln(out, "Each existing tenant gets a new control id for these; owners, evidence,")
	sayln(out, "evaluations, exceptions and links stay on the old row (docs/audit-log/1908).")
	for _, s := range res.Supersedes {
		logged := "no decisions-log entry"
		if s.Logged {
			logged = "named in a changed decisions log"
		}
		say(out, "  %s: %s (%s)\n", s.BundleID, strings.Join(s.Fields, ", "), logged)
	}
	if res.OK() {
		say(out, "Acknowledged: the PR carries %q and every bundle is named in a changed %s/*.md.\n", AckLabel, dlogDir)
		return 0
	}
	say(out, "To proceed on purpose: add the %q label and name each bundle above, with an\n", AckLabel)
	say(out, "upgrade note, in a %s/*.md this PR changes. Otherwise keep the change to\n", dlogDir)
	sayln(out, "in-place fields (description, external_refs, anchor_fit_note, evidence_queries[].description).")
	return 1
}

// baseManifests reads every control.yaml under dir at rev, keyed by
// bundle_id.
func baseManifests(rev, dir string) (map[string][]byte, error) {
	list, err := git("ls-tree", "-r", "--name-only", rev, "--", dir)
	if err != nil {
		return nil, err
	}
	out := map[string][]byte{}
	for _, p := range splitLines(string(list)) {
		if filepath.Base(p) != "control.yaml" {
			continue
		}
		raw, err := git("show", rev+":"+p)
		if err != nil {
			return nil, err
		}
		if err := add(out, p, raw); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// treeManifests reads every control.yaml under dir in the working tree,
// keyed by bundle_id.
func treeManifests(dir string) (map[string][]byte, error) {
	out := map[string][]byte{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Name() != "control.yaml" {
			return nil
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return add(out, p, raw)
	})
	if errors.Is(err, fs.ErrNotExist) {
		return out, nil
	}
	return out, err
}

func add(m map[string][]byte, path string, raw []byte) error {
	var head struct {
		BundleID string `yaml:"bundle_id"`
	}
	if err := yaml.Unmarshal(raw, &head); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if head.BundleID == "" {
		return fmt.Errorf("%s: no bundle_id", path)
	}
	if _, dup := m[head.BundleID]; dup {
		return fmt.Errorf("%s: bundle_id %q appears twice", path, head.BundleID)
	}
	m[head.BundleID] = raw
	return nil
}

// changedDLogs returns the working-tree contents of every decisions log
// that differs from rev (added or modified).
func changedDLogs(rev string) ([][]byte, error) {
	list, err := git("diff", "--name-only", "--diff-filter=AM", rev, "--", dlogDir)
	if err != nil {
		return nil, err
	}
	var untracked []byte
	if untracked, err = git("ls-files", "--others", "--exclude-standard", "--", dlogDir); err != nil {
		return nil, err
	}
	var out [][]byte
	for _, p := range append(splitLines(string(list)), splitLines(string(untracked))...) {
		if !strings.HasSuffix(p, ".md") {
			continue
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		out = append(out, raw)
	}
	return out, nil
}

func git(args ...string) ([]byte, error) {
	var stderr bytes.Buffer
	cmd := exec.Command("git", args...)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// say and sayln write a report line. A failed write to stdout or stderr has
// nowhere better to go, so its error is dropped on purpose.
func say(w io.Writer, format string, args ...any) { _, _ = fmt.Fprintf(w, format, args...) }

func sayln(w io.Writer, s string) { _, _ = fmt.Fprintln(w, s) }

func splitLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}
