// Package scfbind picks the SCF release an OSCAL import matches its control
// ids in, and loads that release's SCF codes (#1771, #2264).
//
// Every OSCAL import that turns a control id into an SCF anchor (catalog,
// profile, component-definition) declares the release the same way: an
// --scf-release flag, or an "scf-release" prop on the document's metadata.
// With neither, the import matches in the current release, as before, and
// reports a deprecation warning until #1774 makes the declaration required.
package scfbind

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/scfrelease"
)

// Prop is the name of the document metadata prop that declares the SCF
// release the document's control ids belong to.
const Prop = "scf-release"

// ErrConflict is returned when --scf-release and the document's
// "scf-release" prop name different SCF releases, or the document names two.
var ErrConflict = errors.New("oscal import: conflicting SCF release declarations")

// PropReleaseNotLoadedWarning is reported when a release named only by the
// document's prop is not loaded and the import falls back to the current
// release (the #1771 D5 fallback). Its verbs are the release, twice.
const PropReleaseNotLoadedWarning = "the document's \"scf-release\" prop names SCF release %q, which is not loaded, so its control ids were matched in the current SCF release; load that release with `atlas-cli catalog import-scf` to match in it, or pass --scf-release %s to refuse the import instead of falling back"

// Declaration is the SCF release an import declares, and where it came from.
type Declaration struct {
	// Release is the declared label, or "" when nothing declares one.
	Release string
	// FromFlag is true when --scf-release named the release. A release
	// named only by the document's prop is the document's statement, not
	// the operator's.
	FromFlag bool
}

// Declared returns the SCF release an import declares: the flag, the
// "scf-release" prop on the document's <root>.metadata, or neither. root is
// the OSCAL document's top-level key ("catalog", "profile",
// "component-definition"). The document is only decoded, never
// dereferenced. A document the decoder cannot read declares nothing: the
// bridge validates it, so that would be a shape this reader does not know,
// not a bad document.
func Declared(flag string, oscalJSON []byte, root string) (Declaration, error) {
	var doc map[string]struct {
		Metadata struct {
			Props []struct {
				Name  string `json:"name"`
				Value string `json:"value"`
			} `json:"props"`
		} `json:"metadata"`
	}
	fromDoc := ""
	if err := json.Unmarshal(oscalJSON, &doc); err == nil {
		for _, p := range doc[root].Metadata.Props {
			if p.Name != Prop {
				continue
			}
			v := strings.TrimSpace(p.Value)
			if fromDoc != "" && v != fromDoc {
				return Declaration{}, fmt.Errorf("%w: the document's metadata has %s props %q and %q", ErrConflict, Prop, fromDoc, v)
			}
			fromDoc = v
		}
	}
	flag = strings.TrimSpace(flag)
	if flag != "" && fromDoc != "" && flag != fromDoc {
		return Declaration{}, fmt.Errorf("%w: --scf-release %q, but the document's %s prop is %q", ErrConflict, flag, Prop, fromDoc)
	}
	if flag != "" {
		return Declaration{Release: flag, FromFlag: true}, nil
	}
	return Declaration{Release: fromDoc}, nil
}

// Policy is how one import treats a declaration.
type Policy struct {
	// Deprecation is the warning reported when nothing declares a release.
	Deprecation string
	// PropFallsBack applies the #1771 D5 fallback to a release named only
	// by the document's prop: when it is not loaded, the import matches in
	// the current release with PropReleaseNotLoadedWarning instead of
	// failing. A release named by --scf-release is always strict.
	PropFallsBack bool
}

// Binding is the release an import matches in and that release's codes.
type Binding struct {
	// Release is the release matched in; zero when no SCF release is
	// loaded and none was declared (every control id is then unmapped).
	Release scfrelease.Release
	// Anchors is the set of scf_id codes in Release.
	Anchors map[string]struct{}
	// Warnings are non-fatal notes for the operator.
	Warnings []string
}

// Bind picks the release d names, or the current one when it names none,
// and loads its codes. A declared release that is not loaded is
// scfrelease.ErrUnknownRelease, unless p.PropFallsBack lets a prop-only
// declaration fall back. scf_anchors is the read-only bundled spine; it is
// never written.
func Bind(ctx context.Context, tx pgx.Tx, d Declaration, p Policy) (Binding, error) {
	var b Binding
	q := dbx.New(tx)
	label := d.Release
	if label == "" {
		b.Warnings = append(b.Warnings, p.Deprecation)
	}
	rel, err := scfrelease.Lookup(ctx, q, label)
	if label != "" && !d.FromFlag && p.PropFallsBack && errors.Is(err, scfrelease.ErrUnknownRelease) {
		b.Warnings = append(b.Warnings, fmt.Sprintf(PropReleaseNotLoadedWarning, label, label))
		rel, err = scfrelease.Lookup(ctx, q, "")
		label = ""
	}
	switch {
	case err == nil:
	case label == "" && errors.Is(err, scfrelease.ErrNoCurrentRelease):
		// No SCF catalog loaded: every control id imports unmapped, as before.
	default:
		return Binding{}, err
	}
	b.Release = rel
	b.Anchors, err = loadAnchorIDs(ctx, tx, rel.ID)
	if err != nil {
		return Binding{}, err
	}
	return b, nil
}

// loadAnchorIDs returns the set of scf_id strings in one SCF release (empty
// for an invalid id, when no release is loaded).
func loadAnchorIDs(ctx context.Context, tx pgx.Tx, release pgtype.UUID) (map[string]struct{}, error) {
	out := make(map[string]struct{})
	if !release.Valid {
		return out, nil
	}
	rows, err := tx.Query(ctx, `SELECT scf_id FROM scf_anchors WHERE framework_version_id = $1`, release)
	if err != nil {
		return nil, fmt.Errorf("scfbind: load scf anchors: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var scfID string
		if err := rows.Scan(&scfID); err != nil {
			return nil, fmt.Errorf("scfbind: scan scf anchor: %w", err)
		}
		out[scfID] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scfbind: iterate scf anchors: %w", err)
	}
	return out, nil
}
