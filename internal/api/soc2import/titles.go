package soc2import

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/mgoodric/security-atlas/data/crosswalks"
)

// SCF workbooks carry requirement IDs only, so a workbook requirement has no
// title or body of its own. A framework version is frozen the moment its
// import commits (the immutability trigger rejects any later UPDATE of a
// requirement), so the title has to be right on first insert (#1730). Titles
// resolve in this order: the operator's --titles file, then the bundled
// crosswalk YAML for the same framework slug, then the code itself, which the
// loader refuses unless SCFWorkbookOptions.AllowCodeTitles is set.

// titleSet maps a requirement code to the requirement carrying its title and
// body.
type titleSet map[string]Requirement

// titlesYAML is the subset of the crosswalk YAML a titles file is read as.
// Mappings, if present, are ignored.
type titlesYAML struct {
	FrameworkSlug string        `yaml:"framework_slug"`
	Requirements  []Requirement `yaml:"requirements"`
}

// pointOfFocusRe matches a SOC 2 point of focus such as "CC1.1-POF3". It
// takes its parent criterion's title (#1730); rolling it up into the parent
// is #1731.
var pointOfFocusRe = regexp.MustCompile(`^(.+)-POF\d+$`)

// CodeOnlyTitlesError lists the requirements a workbook import would title
// with their own code. Nothing is written when the loader returns it.
type CodeOnlyTitlesError struct {
	FrameworkSlug string
	Codes         []string
}

func (e *CodeOnlyTitlesError) Error() string {
	return fmt.Sprintf(
		"crosswalk: %d requirement(s) of %s would be titled with their own code, and a framework version cannot be retitled once imported: %s; pass --titles <yaml> with their titles, or --allow-code-titles to import them as codes",
		len(e.Codes), e.FrameworkSlug, strings.Join(e.Codes, ", "))
}

// loadTitlesFile reads a titles file in the crosswalk YAML requirements
// format (code, title, body).
func loadTitlesFile(p string) (titleSet, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("crosswalk: read titles %s: %w", p, err)
	}
	var doc titlesYAML
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("crosswalk: parse titles %s: %w", p, err)
	}
	if len(doc.Requirements) == 0 {
		return nil, fmt.Errorf("crosswalk: titles %s has zero requirements", p)
	}
	out := make(titleSet, len(doc.Requirements))
	for i, r := range doc.Requirements {
		if r.Code == "" || r.Title == "" {
			return nil, fmt.Errorf("crosswalk: titles %s requirement[%d] needs both code and title", p, i)
		}
		out[r.Code] = r
	}
	return out, nil
}

// bundledTitles returns the titles of the bundled crosswalk YAML whose
// framework_slug is slug, or nil when no bundled file covers it.
func bundledTitles(fsys fs.FS, slug string) (titleSet, error) {
	entries, err := fs.Glob(fsys, "*.yaml")
	if err != nil {
		return nil, fmt.Errorf("crosswalk: list bundled crosswalks: %w", err)
	}
	for _, name := range entries {
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, fmt.Errorf("crosswalk: read bundled %s: %w", name, err)
		}
		var doc titlesYAML
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return nil, fmt.Errorf("crosswalk: parse bundled %s: %w", path.Base(name), err)
		}
		if doc.FrameworkSlug != slug {
			continue
		}
		out := make(titleSet, len(doc.Requirements))
		for _, r := range doc.Requirements {
			out[r.Code] = r
		}
		return out, nil
	}
	return nil, nil
}

// lookup finds code in the sources, first match wins.
func lookup(code string, sources []titleSet) (Requirement, bool) {
	for _, s := range sources {
		if r, ok := s[code]; ok && r.Title != "" {
			return r, true
		}
	}
	return Requirement{}, false
}

// resolveWorkbookTitles titles every workbook requirement from opts.TitlesPath,
// then the bundled crosswalk for cw.FrameworkSlug. A point of focus takes its
// parent criterion's title and no body. A requirement no source covers keeps
// its code as its title; unless opts.AllowCodeTitles is set, that fails the
// load with a CodeOnlyTitlesError.
func resolveWorkbookTitles(cw *Crosswalk, opts SCFWorkbookOptions) error {
	var sources []titleSet
	if opts.TitlesPath != "" {
		override, err := loadTitlesFile(opts.TitlesPath)
		if err != nil {
			return err
		}
		sources = append(sources, override)
	}
	bundled, err := bundledTitles(crosswalks.EmbeddedFS(), cw.FrameworkSlug)
	if err != nil {
		return err
	}
	if bundled != nil {
		sources = append(sources, bundled)
	}

	var codeOnly []string
	for i, r := range cw.Requirements {
		if src, ok := lookup(r.Code, sources); ok {
			cw.Requirements[i].Title = src.Title
			cw.Requirements[i].Body = src.Body
			continue
		}
		if m := pointOfFocusRe.FindStringSubmatch(r.Code); m != nil {
			if parent, ok := lookup(m[1], sources); ok {
				cw.Requirements[i].Title = parent.Title
				continue
			}
		}
		codeOnly = append(codeOnly, r.Code)
	}
	if len(codeOnly) > 0 && !opts.AllowCodeTitles {
		return &CodeOnlyTitlesError{FrameworkSlug: cw.FrameworkSlug, Codes: codeOnly}
	}
	return nil
}

// CodeOnlyTitleCount is how many requirements of cw are titled with their own
// code. The CLI reports it after an --allow-code-titles import.
func CodeOnlyTitleCount(cw *Crosswalk) int {
	n := 0
	for _, r := range cw.Requirements {
		if r.Title == r.Code {
			n++
		}
	}
	return n
}
