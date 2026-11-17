package scfbind

import (
	"errors"
	"strings"
	"testing"
)

// The SCF release an import declares, from the flag or the "scf-release"
// prop on the document's metadata, for each OSCAL root an import reads.
func TestDeclared(t *testing.T) {
	t.Parallel()
	for _, root := range []string{"catalog", "profile", "component-definition"} {
		withProps := func(props string) []byte {
			return []byte(`{"` + root + `":{"metadata":{"title":"t","props":[` + props + `]}}}`)
		}
		prop := func(v string) string { return `{"name":"scf-release","value":"` + v + `"}` }
		for _, tc := range []struct {
			name     string
			flag     string
			doc      []byte
			want     string
			conflict bool
		}{
			{name: "neither", doc: []byte(`{"` + root + `":{"metadata":{}}}`)},
			{name: "flag only", flag: "2026.3", doc: []byte(`{"` + root + `":{}}`), want: "2026.3"},
			{name: "flag trimmed", flag: " 2026.3 ", doc: []byte(`{}`), want: "2026.3"},
			{name: "prop only", doc: withProps(prop("2026.2")), want: "2026.2"},
			{name: "other props ignored", doc: withProps(`{"name":"marking","value":"x"},` + prop("2026.2")), want: "2026.2"},
			{name: "prop under another root ignored", doc: []byte(`{"other":{"metadata":{"props":[` + prop("2026.2") + `]}}}`)},
			{name: "flag agrees with prop", flag: "2026.2", doc: withProps(prop("2026.2")), want: "2026.2"},
			{name: "flag contradicts prop", flag: "2026.3", doc: withProps(prop("2026.2")), conflict: true},
			{name: "two props disagree", doc: withProps(prop("2026.2") + "," + prop("2026.3")), conflict: true},
			{name: "undecodable document declares nothing", doc: []byte(`not json`)},
		} {
			t.Run(root+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				got, err := Declared("testimport", tc.flag, tc.doc, root)
				if tc.conflict {
					if !errors.Is(err, ErrConflict) || !strings.HasPrefix(err.Error(), "testimport: ") {
						t.Fatalf("err = %v; want ErrConflict prefixed with the importer", err)
					}
					return
				}
				if err != nil || got != tc.want {
					t.Fatalf("Declared = %q, %v; want %q", got, err, tc.want)
				}
			})
		}
	}
}
