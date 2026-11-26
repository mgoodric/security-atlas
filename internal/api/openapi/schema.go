// #2394 — response schemas derived from the Go wire structs.
//
// An operation listed in ResponseSchemas documents its 200 body with a
// schema reflected from the struct its handler encodes, instead of the
// generic Ack. The web client generates its TypeScript types from the
// spec (web/lib/api/generated/openapi.d.ts), so a renamed or retyped JSON
// field fails, in order: openapi-drift-check (the committed spec no
// longer matches the regenerated one), the web types drift check, and the
// web typecheck wherever the old shape is still used.
//
// Reflection reads only what encoding/json reads: the json tag name,
// omitempty (an omitempty field is optional, every other field is
// required), ",string" (a quoted bool or number is a string), "-"
// (skipped) and the field's Go type. A type that encodes itself
// (json.Marshaler, encoding.TextMarshaler) is documented only through an
// entry in knownTypes: its Go kind says nothing about its wire shape (a
// uuid.UUID is a [16]byte but encodes as a string). A Go type this
// emitter does not know is an error, never a guess.

package openapi

import (
	"encoding"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ResponseSchema documents one operation's 200 response body.
type ResponseSchema struct {
	Method string
	Path   string
	// Type is the struct the handler passes to httpresp.WriteJSON.
	Type reflect.Type
}

// SchemaEnum pins a string field of a named schema to the values the Go
// source lists, so the generated TypeScript type is a union, not string.
type SchemaEnum struct {
	Schema string // component name, e.g. "RemapProposal"
	Field  string // json name, e.g. "class"
	Values []string
}

// responseSchemaFor returns the component name documenting r's response,
// or "" when r has none and keeps the Ack placeholder.
func responseSchemaFor(r RouteSpec) string {
	for _, s := range ResponseSchemas {
		if s.Method == r.Method && s.Path == r.Path {
			return schemaName(s.Type)
		}
	}
	return ""
}

// schemaName is the component name of a struct type: its entry in
// schemaNames, else its Go type name.
func schemaName(t reflect.Type) string {
	if n, ok := schemaNames[t]; ok {
		return n
	}
	return t.Name()
}

// collectSchemas walks every type ResponseSchemas reaches and returns the
// named structs, keyed by component name. Two distinct Go types with one
// component name is an error.
func collectSchemas() (map[string]reflect.Type, error) {
	out := map[string]reflect.Type{}
	// path names where t sits, from the response struct down, so an
	// error points at the field to fix: "Report.items[].at".
	var walk func(t reflect.Type, path string) error
	walk = func(t reflect.Type, path string) error {
		if _, ok := knownTypes[t]; ok {
			return nil
		}
		if err := refuseMarshaler(t); err != nil {
			return fmt.Errorf("openapi: %s: %w", path, err)
		}
		switch t.Kind() {
		case reflect.Pointer:
			return walk(t.Elem(), path)
		case reflect.Slice, reflect.Array:
			return walk(t.Elem(), path+"[]")
		case reflect.Map:
			return walk(t.Elem(), path+"{}")
		case reflect.Struct:
			name := schemaName(t)
			if name == "" {
				return fmt.Errorf("openapi: %s: anonymous struct %s has no schema name", path, t)
			}
			if prev, ok := out[name]; ok {
				if prev != t {
					return fmt.Errorf("openapi: schema name %q used by both %s and %s", name, prev, t)
				}
				return nil
			}
			out[name] = t
			for _, f := range jsonFields(t) {
				if err := walk(f.Type, path+"."+f.Name); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for _, s := range ResponseSchemas {
		if s.Type.Kind() != reflect.Struct {
			return nil, fmt.Errorf("openapi: response type for %s %s is %s, want a struct", s.Method, s.Path, s.Type)
		}
		if err := walk(s.Type, schemaName(s.Type)); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// knownTypes maps a type that encodes itself to the schema lines of what
// it writes. An empty list is the empty schema: any JSON value.
// schema_test.go checks each entry against encoding/json.
var knownTypes = map[reflect.Type][]string{
	reflect.TypeOf(time.Time{}):       {"type: string", "format: date-time"},
	reflect.TypeOf(uuid.UUID{}):       {"type: string", "format: uuid"},
	reflect.TypeOf(json.RawMessage{}): {},
}

var (
	jsonMarshalerType = reflect.TypeOf((*json.Marshaler)(nil)).Elem()
	textMarshalerType = reflect.TypeOf((*encoding.TextMarshaler)(nil)).Elem()
	stringType        = reflect.TypeOf("")
)

// refuseMarshaler errors when t, or *t, encodes itself and has no
// knownTypes entry (pgtype.*, for example). A pointer is judged by its
// element, which is what encoding/json writes when it is not nil.
func refuseMarshaler(t reflect.Type) error {
	if m := marshalerOf(t); m != nil {
		return fmt.Errorf("%s implements %s; add it to knownTypes with the schema of what it writes", t, m)
	}
	return nil
}

// marshalerOf is the marshaler interface t, or *t, implements, or nil.
// A pointer is never judged itself, only through its element.
func marshalerOf(t reflect.Type) reflect.Type {
	if t.Kind() == reflect.Pointer {
		return nil
	}
	for _, m := range []reflect.Type{jsonMarshalerType, textMarshalerType} {
		if t.Implements(m) || reflect.PointerTo(t).Implements(m) {
			return m
		}
	}
	return nil
}

type jsonField struct {
	Name     string
	Required bool
	Type     reflect.Type

	index  []int // field index sequence from the listed struct
	tagged bool  // the name came from a json tag
}

// embeddedStruct is an anonymous struct whose fields are promoted into
// the struct jsonFields lists.
type embeddedStruct struct {
	typ   reflect.Type
	index []int
	// optional: reached through an embedded pointer. When it is nil,
	// encoding/json omits every field under it.
	optional bool
}

// jsonFields lists t's fields as encoding/json encodes them, in
// declaration order. It follows encoding/json's typeFields: embedded
// structs, and pointers to them, are flattened breadth first, and when
// two fields share a JSON name the shallower one wins, then the tagged
// one; two at one depth that tie on the tag are both dropped.
func jsonFields(t reflect.Type) []jsonField {
	var fields []jsonField
	next := []embeddedStruct{{typ: t}}
	// count and nextCount: how often each struct type is embedded at
	// the current and the next depth.
	var count, nextCount map[reflect.Type]int
	visited := map[reflect.Type]bool{}
	for len(next) > 0 {
		var current []embeddedStruct
		current, next = next, nil
		count, nextCount = nextCount, map[reflect.Type]int{}
		for _, e := range current {
			// A type embedded at two depths is listed at the shallower
			// one only, which also ends embedding cycles.
			if visited[e.typ] {
				continue
			}
			visited[e.typ] = true
			for i := 0; i < e.typ.NumField(); i++ {
				f := e.typ.Field(i)
				if f.Anonymous {
					et := f.Type
					if et.Kind() == reflect.Pointer {
						et = et.Elem()
					}
					// An unexported embedded struct's exported fields
					// are promoted; an unexported embedded non-struct is
					// skipped.
					if !f.IsExported() && et.Kind() != reflect.Struct {
						continue
					}
				} else if !f.IsExported() {
					continue
				}
				tag := f.Tag.Get("json")
				if tag == "-" {
					continue
				}
				name, opts, _ := strings.Cut(tag, ",")
				index := append(append([]int(nil), e.index...), i)
				ft := f.Type
				if ft.Name() == "" && ft.Kind() == reflect.Pointer {
					ft = ft.Elem()
				}
				if name == "" && f.Anonymous && ft.Kind() == reflect.Struct {
					nextCount[ft]++
					if nextCount[ft] == 1 {
						next = append(next, embeddedStruct{
							typ:      ft,
							index:    index,
							optional: e.optional || f.Type.Kind() == reflect.Pointer,
						})
					}
					continue
				}
				omit, quoted := false, false
				for _, o := range strings.Split(opts, ",") {
					switch o {
					case "omitempty", "omitzero":
						omit = true
					case "string":
						quoted = true
					}
				}
				jf := jsonField{
					Name:     name,
					Required: !omit && !e.optional,
					Type:     f.Type,
					index:    index,
					tagged:   name != "",
				}
				if jf.Name == "" {
					jf.Name = f.Name
				}
				if quoted {
					jf.Type = quotedType(f.Type)
				}
				fields = append(fields, jf)
				// One struct embedded twice at this depth: its fields
				// collide with each other, so a second copy makes the
				// dominance pass below drop both.
				if count[e.typ] > 1 {
					fields = append(fields, jf)
				}
			}
		}
	}

	sort.Slice(fields, func(i, j int) bool {
		a, b := fields[i], fields[j]
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		if len(a.index) != len(b.index) {
			return len(a.index) < len(b.index)
		}
		if a.tagged != b.tagged {
			return a.tagged
		}
		return compareIndex(a.index, b.index) < 0
	})
	out := fields[:0]
	for i := 0; i < len(fields); {
		j := i + 1
		for j < len(fields) && fields[j].Name == fields[i].Name {
			j++
		}
		// The first of a name dominates unless the second ties it on
		// both depth and tag.
		if j-i == 1 || len(fields[i].index) != len(fields[i+1].index) || fields[i].tagged != fields[i+1].tagged {
			out = append(out, fields[i])
		}
		i = j
	}
	sort.Slice(out, func(i, j int) bool { return compareIndex(out[i].index, out[j].index) < 0 })
	return out
}

// compareIndex orders two field index sequences as declaration order.
func compareIndex(a, b []int) int {
	for k := 0; k < len(a) && k < len(b); k++ {
		if a[k] != b[k] {
			return a[k] - b[k]
		}
	}
	return len(a) - len(b)
}

// quotedType is the type a ",string" field is on the wire. encoding/json
// quotes a bool, integer or float, directly or through an unnamed
// pointer; on any other type the option has no effect. Nor does it on a
// type that encodes itself: the marshaler wins, so the type is kept for
// refuseMarshaler to judge.
func quotedType(t reflect.Type) reflect.Type {
	elem, ptr := t, false
	if t.Name() == "" && t.Kind() == reflect.Pointer {
		elem, ptr = t.Elem(), true
	}
	if marshalerOf(elem) != nil {
		return t
	}
	switch elem.Kind() {
	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64:
		if ptr {
			return reflect.PointerTo(stringType)
		}
		return stringType
	}
	return t
}

// writeSchemaComponents emits every collected schema under
// components.schemas, sorted by name.
func writeSchemaComponents(w io.Writer) error {
	schemas, err := collectSchemas()
	if err != nil {
		return err
	}
	names := make([]string, 0, len(schemas))
	for n := range schemas {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if err := writeStructSchema(w, n, schemas[n]); err != nil {
			return err
		}
	}
	return nil
}

func writeStructSchema(w io.Writer, name string, t reflect.Type) error {
	fields := jsonFields(t)
	var b strings.Builder
	fmt.Fprintf(&b, "    %s:\n", name)
	b.WriteString("      type: object\n")
	var req []string
	for _, f := range fields {
		if f.Required {
			req = append(req, f.Name)
		}
	}
	// Block lists, not flow ones: prettier rewraps a long flow list, and
	// the committed spec must stay byte-identical to this output.
	if len(req) > 0 {
		b.WriteString("      required:\n")
		for _, n := range req {
			fmt.Fprintf(&b, "        - %s\n", n)
		}
	}
	b.WriteString("      additionalProperties: false\n")
	if len(fields) > 0 {
		b.WriteString("      properties:\n")
	}
	for _, f := range fields {
		fmt.Fprintf(&b, "        %s:\n", f.Name)
		if vals := enumFor(name, f.Name); vals != nil {
			if f.Type.Kind() != reflect.String {
				return fmt.Errorf("openapi: enum on %s.%s, which is %s, not a string", name, f.Name, f.Type)
			}
			b.WriteString("          type: string\n")
			b.WriteString("          enum:\n")
			for _, v := range vals {
				fmt.Fprintf(&b, "            - %s\n", v)
			}
			continue
		}
		if err := writeTypeSchema(&b, f.Type, "          "); err != nil {
			return fmt.Errorf("openapi: %s.%s: %w", name, f.Name, err)
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func enumFor(schema, field string) []string {
	for _, e := range SchemaEnums {
		if e.Schema == schema && e.Field == field {
			return e.Values
		}
	}
	return nil
}

// writeTypeSchema writes the schema body for t at indent.
func writeTypeSchema(b *strings.Builder, t reflect.Type, indent string) error {
	if lines, ok := knownTypes[t]; ok {
		if len(lines) == 0 {
			fmt.Fprintf(b, "%s{}\n", indent)
		}
		for _, l := range lines {
			fmt.Fprintf(b, "%s%s\n", indent, l)
		}
		return nil
	}
	if err := refuseMarshaler(t); err != nil {
		return err
	}
	switch t.Kind() {
	case reflect.String:
		fmt.Fprintf(b, "%stype: string\n", indent)
	case reflect.Bool:
		fmt.Fprintf(b, "%stype: boolean\n", indent)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		fmt.Fprintf(b, "%stype: integer\n", indent)
	case reflect.Float32, reflect.Float64:
		fmt.Fprintf(b, "%stype: number\n", indent)
	case reflect.Slice, reflect.Array:
		if t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8 {
			// encoding/json writes a []byte as a base64 string.
			fmt.Fprintf(b, "%stype: string\n", indent)
			fmt.Fprintf(b, "%scontentEncoding: base64\n", indent)
			return nil
		}
		// A nil slice encodes as null; the wire structs listed in
		// ResponseSchemas always send an array, which is what this says.
		fmt.Fprintf(b, "%stype: array\n", indent)
		fmt.Fprintf(b, "%sitems:\n", indent)
		return writeTypeSchema(b, t.Elem(), indent+"  ")
	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			return fmt.Errorf("map key %s is not a string", t.Key())
		}
		fmt.Fprintf(b, "%stype: object\n", indent)
		fmt.Fprintf(b, "%sadditionalProperties:\n", indent)
		return writeTypeSchema(b, t.Elem(), indent+"  ")
	case reflect.Pointer:
		fmt.Fprintf(b, "%soneOf:\n", indent)
		fmt.Fprintf(b, "%s  - type: \"null\"\n", indent)
		var inner strings.Builder
		if err := writeTypeSchema(&inner, t.Elem(), indent+"    "); err != nil {
			return err
		}
		// First line of the inner schema becomes the list item.
		s := inner.String()
		fmt.Fprintf(b, "%s  - %s", indent, strings.TrimPrefix(s, indent+"    "))
	case reflect.Struct:
		fmt.Fprintf(b, "%s$ref: \"#/components/schemas/%s\"\n", indent, schemaName(t))
	default:
		return fmt.Errorf("unsupported Go type %s", t)
	}
	return nil
}
