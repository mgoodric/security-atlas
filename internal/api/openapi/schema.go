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
// required), "-" (skipped) and the field's Go type. A Go type this
// emitter does not know is an error, never a guess.

package openapi

import (
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"time"
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
	var walk func(t reflect.Type) error
	walk = func(t reflect.Type) error {
		switch t.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Map:
			return walk(t.Elem())
		case reflect.Struct:
			if t == timeType {
				return nil
			}
			name := schemaName(t)
			if name == "" {
				return fmt.Errorf("openapi: anonymous struct %s has no schema name", t)
			}
			if prev, ok := out[name]; ok {
				if prev != t {
					return fmt.Errorf("openapi: schema name %q used by both %s and %s", name, prev, t)
				}
				return nil
			}
			out[name] = t
			for _, f := range jsonFields(t) {
				if err := walk(f.Type); err != nil {
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
		if err := walk(s.Type); err != nil {
			return nil, err
		}
	}
	return out, nil
}

var timeType = reflect.TypeOf(time.Time{})

type jsonField struct {
	Name     string
	Required bool
	Type     reflect.Type
}

// jsonFields lists t's fields as encoding/json encodes them, in
// declaration order. Embedded fields are flattened.
func jsonFields(t reflect.Type) []jsonField {
	var out []jsonField
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		if f.Anonymous && name == "" && f.Type.Kind() == reflect.Struct {
			out = append(out, jsonFields(f.Type)...)
			continue
		}
		if name == "" {
			name = f.Name
		}
		omit := false
		for _, o := range strings.Split(opts, ",") {
			if o == "omitempty" || o == "omitzero" {
				omit = true
			}
		}
		out = append(out, jsonField{Name: name, Required: !omit, Type: f.Type})
	}
	return out
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
		if t == timeType {
			fmt.Fprintf(b, "%stype: string\n", indent)
			fmt.Fprintf(b, "%sformat: date-time\n", indent)
			return nil
		}
		fmt.Fprintf(b, "%s$ref: \"#/components/schemas/%s\"\n", indent, schemaName(t))
	default:
		return fmt.Errorf("unsupported Go type %s", t)
	}
	return nil
}
