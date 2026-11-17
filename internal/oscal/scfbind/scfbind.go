// Package scfbind picks the SCF release an OSCAL import matches its control
// ids in, and loads that release's SCF codes (#1771, #2264).
//
// Every OSCAL import that turns a control id into an SCF anchor (catalog,
// profile, component-definition) declares the release the same way: an
// --scf-release flag, or an "scf-release" prop on the document's metadata.
// Both are explicit declarations, so a declared release that is not loaded
// fails the import; it never falls back to the current release. With
// neither, the import matches in the current release, as before, and
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
var ErrConflict = errors.New("conflicting SCF release declarations")

// Declared returns the SCF release an import declares: the flag, the
// "scf-release" prop on the document's <root>.metadata, or "" for neither.
// root is the OSCAL document's top-level key ("catalog", "profile",
// "component-definition"); importer prefixes a conflict error (e.g.
// "catalogimport"). The document is only decoded, never dereferenced. A
// document the decoder cannot read declares nothing: the bridge validates
// it, so that would be a shape this reader does not know, not a bad
// document.
func Declared(importer, flag string, oscalJSON []byte, root string) (string, error) {
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
				return "", fmt.Errorf("%s: %w: the document's metadata has %s props %q and %q", importer, ErrConflict, Prop, fromDoc, v)
			}
			fromDoc = v
		}
	}
	flag = strings.TrimSpace(flag)
	if flag != "" && fromDoc != "" && flag != fromDoc {
		return "", fmt.Errorf("%s: %w: --scf-release %q, but the document's %s prop is %q", importer, ErrConflict, flag, Prop, fromDoc)
	}
	if flag != "" {
		return flag, nil
	}
	return fromDoc, nil
}

// Binding is the release an import matches in and that release's codes.
type Binding struct {
	// Release is the release matched in; zero when no SCF release is
	// loaded and none was declared (every control id is then unmapped).
	Release scfrelease.Release
	// Anchors is the set of scf_id codes in Release.
	Anchors map[string]struct{}
	// Warnings are non-fatal notes for the operator: deprecation when no
	// release was declared.
	Warnings []string
}

// Bind picks the declared release, or the current one when release is "",
// and loads its codes. A declared release that is not loaded is
// scfrelease.ErrUnknownRelease. deprecation is the warning reported when
// nothing declares a release. scf_anchors is the read-only bundled spine;
// it is never written.
func Bind(ctx context.Context, tx pgx.Tx, release, deprecation string) (Binding, error) {
	var b Binding
	if release == "" {
		b.Warnings = append(b.Warnings, deprecation)
	}
	rel, err := scfrelease.Lookup(ctx, dbx.New(tx), release)
	switch {
	case err == nil:
	case release == "" && errors.Is(err, scfrelease.ErrNoCurrentRelease):
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
