package main

import (
	"fmt"
	"io"
	"sort"

	"github.com/mgoodric/security-atlas/internal/api/scfimport"
	"github.com/mgoodric/security-atlas/internal/control"
	"github.com/mgoodric/security-atlas/internal/scfrelease"
)

// reportSCFRelease prints the bundle's declared SCF release and, given SCF
// catalogs, flags its scf_anchor_id when the code's meaning changed in a
// release later than the declared one (#1770). "Later" is by release_date,
// then by the order the catalogs were given. Flags are warnings: the
// bundle still validates.
func reportSCFRelease(out io.Writer, m *control.Manifest, catalogPaths []string) error {
	switch {
	case m.MissingSCFRelease():
		_, _ = fmt.Fprintf(out, "    scf_release=(none) ERROR: scf_anchor_id %s is an SCF code but control.yaml declares no scf_release; upload refuses it. Add one line naming the release the code is from, e.g. `scf_release: test-2026.3`\n", m.SCFAnchorID)
	case m.SCFRelease == "":
		_, _ = fmt.Fprintf(out, "    scf_release=(none)\n")
	default:
		_, _ = fmt.Fprintf(out, "    scf_release=%s\n", m.SCFRelease)
	}
	if len(catalogPaths) == 0 {
		return nil
	}

	cats := make([]*scfimport.Catalog, 0, len(catalogPaths))
	for _, p := range catalogPaths {
		cat, err := scfimport.Load(p)
		if err != nil {
			return err
		}
		cats = append(cats, cat)
	}
	sort.SliceStable(cats, func(i, j int) bool {
		a, b := cats[i].ReleaseDate, cats[j].ReleaseDate
		return a != "" && b != "" && a < b
	})

	if m.SCFRelease == "" || m.SCFAnchorID == "" {
		return nil
	}
	declared := -1
	for i, c := range cats {
		if c.ReleaseVersion == m.SCFRelease {
			declared = i
		}
	}
	if declared < 0 {
		_, _ = fmt.Fprintf(out, "    WARN scf_release %s is not among the --scf-catalog releases; release changes not checked\n", m.SCFRelease)
		return nil
	}
	from := catalogAnchors(cats[declared])
	if !hasCode(from, m.SCFAnchorID) {
		_, _ = fmt.Fprintf(out, "    WARN scf_anchor_id %s is not in SCF release %s\n", m.SCFAnchorID, m.SCFRelease)
		return nil
	}
	for _, later := range cats[declared+1:] {
		ch := scfrelease.Classify(from, catalogAnchors(later))[m.SCFAnchorID]
		if !ch.Kind.Changed() {
			continue
		}
		_, _ = fmt.Fprintf(out, "    FLAG scf_anchor_id %s changed in SCF release %s: %s%s\n",
			m.SCFAnchorID, later.ReleaseVersion, ch.Kind, changeDetail(ch))
	}
	return nil
}

func catalogAnchors(c *scfimport.Catalog) []scfrelease.Anchor {
	out := make([]scfrelease.Anchor, 0, len(c.Controls))
	for _, a := range c.Controls {
		out = append(out, scfrelease.Anchor{Code: a.SCFID, Title: a.Title, LegacyCode: a.LegacySCFID})
	}
	return out
}

func hasCode(anchors []scfrelease.Anchor, code string) bool {
	for _, a := range anchors {
		if a.Code == code {
			return true
		}
	}
	return false
}

func changeDetail(ch scfrelease.Change) string {
	switch ch.Kind {
	case scfrelease.KindRenamed:
		return fmt.Sprintf(" (title %q -> %q)", ch.From.Title, ch.Later.Title)
	case scfrelease.KindReused:
		return fmt.Sprintf(" (now %q, from legacy %s)", ch.Later.Title, ch.Later.LegacyCode)
	default:
		return ""
	}
}
