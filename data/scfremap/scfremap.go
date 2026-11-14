// Package scfremap embeds the concept remaps between releases of the bundled
// SCF sample catalog (remaps.json). When a new SCF release is imported, a
// control left on an anchor of the previous release that the upgrade could
// not re-bind is reported as needing re-anchoring; this table supplies the
// note that says where its concept moved (#1971). The rows come from the D5
// table in docs/audit-log/1717-scf-pin-ratchet-decisions.md.
package scfremap

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sync"
)

//go:embed remaps.json
var raw []byte

// Remap is one row: the concept of SCF code From in release FromRelease is
// carried by To in ToRelease.
type Remap struct {
	FromRelease string `json:"from_release"`
	ToRelease   string `json:"to_release"`
	From        string `json:"from"`
	To          string `json:"to"`
	// Basis is "legacy" (SCF lists From as To's legacy ID) or "concept" (the
	// target was chosen by meaning).
	Basis string `json:"basis"`
}

type key struct{ release, scfID string }

var (
	once    sync.Once
	byKey   map[key]Remap
	loadErr error
)

// Parse decodes and validates a remap table.
func Parse(b []byte) (map[key]Remap, error) {
	var doc struct {
		Remaps []Remap `json:"remaps"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("scfremap: parse: %w", err)
	}
	out := make(map[key]Remap, len(doc.Remaps))
	for i, r := range doc.Remaps {
		if r.FromRelease == "" || r.ToRelease == "" || r.From == "" || r.To == "" {
			return nil, fmt.Errorf("scfremap: row %d is missing a field: %+v", i, r)
		}
		if r.Basis != "legacy" && r.Basis != "concept" {
			return nil, fmt.Errorf("scfremap: row %d basis %q is not legacy or concept", i, r.Basis)
		}
		k := key{r.FromRelease, r.From}
		if _, dup := out[k]; dup {
			return nil, fmt.Errorf("scfremap: duplicate row for %s in %s", r.From, r.FromRelease)
		}
		out[k] = r
	}
	return out, nil
}

// Lookup returns the remap for SCF code scfID in release fromRelease.
func Lookup(fromRelease, scfID string) (Remap, bool, error) {
	once.Do(func() { byKey, loadErr = Parse(raw) })
	if loadErr != nil {
		return Remap{}, false, loadErr
	}
	r, ok := byKey[key{fromRelease, scfID}]
	return r, ok, nil
}

// Note renders a remap for an operator: "IAC-21 -> IAC-10 (concept)".
func (r Remap) Note() string {
	return fmt.Sprintf("%s -> %s in %s (%s)", r.From, r.To, r.ToRelease, r.Basis)
}
