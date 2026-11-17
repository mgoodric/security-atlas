package soc2import

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// ConvergeResult is the outcome of converging one bundled crosswalk file.
// Skipped is true when the install never imported the file's framework;
// Err is the load or import failure for this file alone.
type ConvergeResult struct {
	File          string
	FrameworkSlug string
	Skipped       bool
	Report        Report
	Err           error
}

// ConvergeImported re-imports every crosswalk YAML in dir whose framework
// this install has already imported, so an upgrade converges each one to
// the bundled file rather than only SOC 2 (#2014). A framework with no
// platform framework version is skipped: boot never imports a framework the
// operator did not choose.
//
// Each file goes through Import unchanged, so a frozen version keeps its
// stored text, the per-source retire pass applies, and edges another source
// wrote (an approved remap, a differently named YAML, a workbook) are kept.
// Tenant overlay edges live in another table and are never read. A failure
// in one file is recorded on its result and the rest still run; only a
// failure to list dir is returned as an error. Files listed in exclude
// (base names) are left out.
//
// pool MUST be the admin pool (BYPASSRLS), as for Import.
func ConvergeImported(ctx context.Context, pool *pgxpool.Pool, dir string, exclude ...string) ([]ConvergeResult, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return nil, fmt.Errorf("crosswalk: list %s: %w", dir, err)
	}
	sort.Strings(paths)
	skip := make(map[string]bool, len(exclude))
	for _, e := range exclude {
		skip[e] = true
	}
	q := dbx.New(pool)
	var out []ConvergeResult
	for _, path := range paths {
		base := filepath.Base(path)
		if skip[base] {
			continue
		}
		res := ConvergeResult{File: base}
		cw, err := Load(path)
		if err != nil {
			res.Err = err
			out = append(out, res)
			continue
		}
		res.FrameworkSlug = cw.FrameworkSlug
		versions, err := q.ListFrameworkVersionsBySlug(ctx, cw.FrameworkSlug)
		if err != nil {
			res.Err = fmt.Errorf("crosswalk: look up framework %q: %w", cw.FrameworkSlug, err)
			out = append(out, res)
			continue
		}
		if len(versions) == 0 {
			res.Skipped = true
			out = append(out, res)
			continue
		}
		res.Report, res.Err = Import(ctx, pool, cw)
		out = append(out, res)
	}
	return out, nil
}
