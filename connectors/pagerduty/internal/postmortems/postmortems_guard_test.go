package postmortems

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

// TestMetadataOnly_StructuralGuard is the load-bearing over-collection guard
// (P0-538). It walks every field of every connector-side record type by
// reflection and FAILS THE BUILD if any field name suggests it could hold the
// postmortem narrative, timeline, root-cause prose, customer data, responder
// PII, or an action-item title/description. The over-collection boundary is
// structural: if the struct physically can't hold the narrative, the narrative
// can't leak. This test is the tripwire that keeps it that way — the moment a
// future change adds a `Body`/`Narrative`/`Title`/`RootCause` field to capture
// "just a bit more context", this test goes red.
//
// Mirrors the slice-489 metadata-only discipline (the incident/oncall types
// carry no free-text field BY CONSTRUCTION); here it is enforced by an explicit
// reflection assertion because the postmortem over-collection risk is DOMINANT.
func TestMetadataOnly_StructuralGuard(t *testing.T) {
	t.Parallel()

	// Field-name substrings that signal a free-text / PII / narrative slot.
	// Any field whose lower-cased name contains one of these is a violation.
	banned := []string{
		"body", "narrative", "timeline", "title", "description", "summary",
		"cause", "rootcause", "note", "text", "detail", "comment", "message",
		"email", "phone", "contact", "name", "author", "assignee", "reporter",
		"customer", "content", "prose", "story",
	}

	// The exhaustive set of connector-side record types that flow toward a
	// pushed evidence record. Each must be metadata-only.
	types := []reflect.Type{
		reflect.TypeOf(RawPostmortem{}),
		reflect.TypeOf(Postmortem{}),
		reflect.TypeOf(RawActionItem{}),
	}

	timeType := reflect.TypeOf(time.Time{})

	var walk func(prefix string, rt reflect.Type)
	walk = func(prefix string, rt reflect.Type) {
		switch rt.Kind() {
		case reflect.Slice, reflect.Array, reflect.Pointer:
			walk(prefix, rt.Elem())
			return
		case reflect.Struct:
			if rt == timeType {
				return // time.Time is a metadata timestamp, not free-text.
			}
			for i := 0; i < rt.NumField(); i++ {
				f := rt.Field(i)
				lname := strings.ToLower(f.Name)
				for _, b := range banned {
					if strings.Contains(lname, b) {
						t.Errorf("%s.%s: field name contains banned free-text/PII token %q — postmortem records must be metadata-only (P0-538). If you need more context, you almost certainly do NOT; reconsider.", prefix, f.Name, b)
					}
				}
				// A bare string field that survives the name check is still a
				// risk surface: assert the only string fields are the known
				// metadata id/status fields. Any NEW string field must be
				// justified here explicitly.
				if f.Type.Kind() == reflect.String {
					allowed := map[string]bool{"ID": true, "IncidentID": true, "Status": true}
					if !allowed[f.Name] {
						t.Errorf("%s.%s: unexpected string field — only opaque-id / status metadata strings are allowed on a postmortem record (P0-538). Add it to the allow-list ONLY if it is a non-free-text opaque identifier.", prefix, f.Name)
					}
				}
				walk(prefix+"."+f.Name, f.Type)
			}
		default:
			// int/bool/etc. cannot hold narrative free-text.
		}
	}

	for _, rt := range types {
		walk(rt.Name(), rt)
	}
}
