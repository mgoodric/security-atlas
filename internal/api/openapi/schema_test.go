package openapi

import (
	"bytes"
	"encoding/json"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type schemaInner struct {
	Code string `json:"code"`
}

type schemaSample struct {
	ID       string            `json:"id"`
	Count    int64             `json:"count"`
	Ratio    float64           `json:"ratio"`
	Done     bool              `json:"done"`
	Tags     []string          `json:"tags"`
	Next     string            `json:"next,omitempty"`
	Labels   map[string]string `json:"labels"`
	Inner    schemaInner       `json:"inner"`
	MaybeRef *schemaInner      `json:"maybe_ref"`
	MaybeStr *string           `json:"maybe_str"`
	At       time.Time         `json:"at"`
	Skipped  string            `json:"-"`
	hidden   string            // unexported: encoding/json skips it, so must the schema
}

var _ = schemaSample{hidden: "never encoded"}

func withSchemas(t *testing.T, rs []ResponseSchema, enums []SchemaEnum) {
	t.Helper()
	prevR, prevE := ResponseSchemas, SchemaEnums
	ResponseSchemas, SchemaEnums = rs, enums
	t.Cleanup(func() { ResponseSchemas, SchemaEnums = prevR, prevE })
}

func TestSchemaComponentsReflectJSONTags(t *testing.T) {
	withSchemas(t, []ResponseSchema{{Method: "GET", Path: "/x", Type: reflect.TypeOf(schemaSample{})}},
		[]SchemaEnum{{Schema: "schemaInner", Field: "code", Values: []string{"a", "b"}}})
	var buf bytes.Buffer
	if err := writeSchemaComponents(&buf); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	for _, want := range []string{
		"    schemaInner:\n",
		"          enum:\n            - a\n            - b\n",
		"    schemaSample:\n",
		"      required:\n        - id\n        - count\n        - ratio\n        - done\n        - tags\n        - labels\n        - inner\n        - maybe_ref\n        - maybe_str\n        - at\n      additionalProperties: false\n",
		"        count:\n          type: integer\n",
		"        ratio:\n          type: number\n",
		"        done:\n          type: boolean\n",
		"        tags:\n          type: array\n          items:\n            type: string\n",
		"        labels:\n          type: object\n          additionalProperties:\n            type: string\n",
		"        inner:\n          $ref: \"#/components/schemas/schemaInner\"\n",
		"        maybe_ref:\n          oneOf:\n            - type: \"null\"\n            - $ref: \"#/components/schemas/schemaInner\"\n",
		"        maybe_str:\n          oneOf:\n            - type: \"null\"\n            - type: string\n",
		"        at:\n          type: string\n          format: date-time\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("schema missing %q in:\n%s", want, got)
		}
	}
	for _, banned := range []string{"Skipped", "hidden"} {
		if strings.Contains(got, banned) {
			t.Errorf("schema documents unencoded field %q", banned)
		}
	}
	// schemaInner sorts before schemaSample.
	if strings.Index(got, "schemaInner:") > strings.Index(got, "schemaSample:") {
		t.Error("components are not sorted by name")
	}
}

func TestSchemaComponentsRefuseUnknownTypes(t *testing.T) {
	type bad struct {
		C chan int `json:"c"`
	}
	withSchemas(t, []ResponseSchema{{Method: "GET", Path: "/x", Type: reflect.TypeOf(bad{})}}, nil)
	if err := writeSchemaComponents(&bytes.Buffer{}); err == nil {
		t.Fatal("want an error for a chan field")
	}
}

type schemaEmbedded struct {
	Extra string `json:"extra"`
}

type schemaWire struct {
	*schemaEmbedded
	ID      uuid.UUID       `json:"id"`
	MaybeID *uuid.UUID      `json:"maybe_id"`
	Raw     json.RawMessage `json:"raw"`
	Blob    []byte          `json:"blob"`
	Big     int64           `json:"big,string"`
	MaybeN  *int            `json:"maybe_n,string"`
	Flag    bool            `json:"flag,string,omitempty"`
	Name    string          `json:"name,string"`
}

// #2622: types that encode themselves, ",string" and embedded pointers
// are documented as encoding/json writes them.
func TestSchemaComponentsFollowEncodingJSON(t *testing.T) {
	withSchemas(t, []ResponseSchema{{Method: "GET", Path: "/x", Type: reflect.TypeOf(schemaWire{})}}, nil)
	var buf bytes.Buffer
	if err := writeSchemaComponents(&buf); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	for _, want := range []string{
		// extra comes from a nil-able embedded pointer, so it is optional.
		"      required:\n        - id\n        - maybe_id\n        - raw\n        - blob\n        - big\n        - maybe_n\n        - name\n      additionalProperties: false\n",
		"        extra:\n          type: string\n",
		"        id:\n          type: string\n          format: uuid\n",
		"        maybe_id:\n          oneOf:\n            - type: \"null\"\n            - type: string\n              format: uuid\n",
		"        raw:\n          {}\n",
		"        blob:\n          type: string\n          contentEncoding: base64\n",
		"        big:\n          type: string\n",
		"        maybe_n:\n          oneOf:\n            - type: \"null\"\n            - type: string\n",
		"        flag:\n          type: string\n",
		"        name:\n          type: string\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("schema missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "schemaEmbedded:") {
		t.Error("embedded pointer struct documented as its own component, not flattened")
	}
	if strings.Contains(got, "integer") {
		t.Errorf("a uuid or ,string field documented as an integer:\n%s", got)
	}

	// The wire agrees: encoding/json writes each of these as a string.
	n := 7
	enc, err := json.Marshal(schemaWire{
		schemaEmbedded: &schemaEmbedded{Extra: "e"},
		ID:             uuid.New(),
		Raw:            json.RawMessage(`{"a":1}`),
		Blob:           []byte("hi"),
		Big:            42,
		MaybeN:         &n,
		Flag:           true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(enc, &wire); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"extra", "id", "blob", "big", "maybe_n", "flag", "name"} {
		if _, ok := wire[k].(string); !ok {
			t.Errorf("encoding/json wrote %s as %T, the schema says string", k, wire[k])
		}
	}
	if _, ok := wire["raw"].(map[string]any); !ok {
		t.Errorf("encoding/json wrote raw as %T, want the embedded object", wire["raw"])
	}
}

func TestSchemaComponentsRefuseUnmappedMarshalers(t *testing.T) {
	type withPgtype struct {
		At pgtype.Timestamptz `json:"at"`
	}
	type withText struct {
		IP net.IP `json:"ip"`
	}
	type withPtr struct {
		S *pgtype.Text `json:"s"`
	}
	type inSlice struct {
		Xs []pgtype.UUID `json:"xs"`
	}
	for _, typ := range []reflect.Type{
		reflect.TypeOf(withPgtype{}), reflect.TypeOf(withText{}),
		reflect.TypeOf(withPtr{}), reflect.TypeOf(inSlice{}),
	} {
		withSchemas(t, []ResponseSchema{{Method: "GET", Path: "/x", Type: typ}}, nil)
		err := writeSchemaComponents(&bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "knownTypes") {
			t.Errorf("%s: want a knownTypes error, got %v", typ, err)
		}
	}
}

// #2716: a named number that encodes itself keeps its marshaler under
// ",string", so it is refused, not documented as a string.
type schemaCents int64

func (c schemaCents) MarshalJSON() ([]byte, error) { return []byte(`{"cents":1}`), nil }

func TestSchemaComponentsMarshalerBeatsQuoted(t *testing.T) {
	type quotedMarshaler struct {
		Price schemaCents `json:"price,string"`
	}
	enc, err := json.Marshal(quotedMarshaler{Price: 1})
	if err != nil {
		t.Fatal(err)
	}
	if string(enc) != `{"price":{"cents":1}}` {
		t.Fatalf("encoding/json wrote %s; the premise of this test no longer holds", enc)
	}
	withSchemas(t, []ResponseSchema{{Method: "GET", Path: "/x", Type: reflect.TypeOf(quotedMarshaler{})}}, nil)
	err = writeSchemaComponents(&bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "knownTypes") {
		t.Fatalf("want a knownTypes error, got %v", err)
	}
}

// #2716: the marshaler error names the schema and the field path.
func TestSchemaComponentsMarshalerErrorNamesThePath(t *testing.T) {
	type schemaLeaf struct {
		At pgtype.Timestamptz `json:"at"`
	}
	type schemaRoot struct {
		Items []schemaLeaf `json:"items"`
	}
	withSchemas(t, []ResponseSchema{{Method: "GET", Path: "/x", Type: reflect.TypeOf(schemaRoot{})}}, nil)
	err := writeSchemaComponents(&bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "openapi: schemaRoot.items[].at: pgtype.Timestamptz implements") {
		t.Fatalf("want the path schemaRoot.items[].at in the error, got %v", err)
	}
}

type schemaDeep struct {
	Shadowed string `json:"shadowed"`
	Deep     string `json:"deep"`
}

type schemaMid struct {
	schemaDeep
	Shadowed int `json:"shadowed"`
}

type schemaTagA struct {
	Name string `json:"Name"`
}

type schemaTagB struct {
	Name int // untagged: loses to schemaTagA.Name at the same depth
}

type schemaSelf struct {
	// An embedding cycle: listed once, at the shallowest depth.
	*schemaSelf
	Loop string `json:"loop"`
}

var _ = schemaSelf{schemaSelf: nil}

// schemaConflict exercises encoding/json's dominance rules: the
// shallower field wins, a tagged field beats an untagged one at the same
// depth, and a tie drops both.
type schemaConflict struct {
	schemaMid
	schemaTagA
	schemaTagB
	schemaSelf
	Deep string `json:"deep_outer"`
	Top  bool   `json:"shadowed"`
}

// #2716: field-name conflicts resolve as encoding/json resolves them.
func TestJSONFieldsFollowEncodingJSONDominance(t *testing.T) {
	fields := jsonFields(reflect.TypeOf(schemaConflict{}))
	got := map[string]jsonField{}
	var names []string
	for _, f := range fields {
		if _, dup := got[f.Name]; dup {
			t.Errorf("JSON name %q listed twice", f.Name)
		}
		got[f.Name] = f
		names = append(names, f.Name)
	}

	enc, err := json.Marshal(schemaConflict{})
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(enc, &wire); err != nil {
		t.Fatal(err)
	}
	if len(wire) != len(got) {
		t.Errorf("schema lists %v; encoding/json writes %s", names, enc)
	}
	for k := range wire {
		if _, ok := got[k]; !ok {
			t.Errorf("encoding/json writes %q, the schema omits it (%v)", k, names)
		}
	}
	if f := got["shadowed"]; f.Type != reflect.TypeOf(false) {
		t.Errorf("shadowed is %v, want the outer bool", f.Type)
	}
	if f := got["Name"]; f.Type != reflect.TypeOf("") || !f.tagged {
		t.Errorf("Name should come from the tagged schemaTagA.Name, got %+v", f)
	}
	if _, ok := wire["Name"].(string); !ok {
		t.Errorf("encoding/json wrote Name as %T, want string", wire["Name"])
	}
	if _, ok := wire["shadowed"].(bool); !ok {
		t.Errorf("encoding/json wrote shadowed as %T, want bool", wire["shadowed"])
	}
	// Declaration order survives the dominance pass.
	want := []string{"deep", "Name", "loop", "deep_outer", "shadowed"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("order %v, want %v", names, want)
	}
}

type SchemaTieA struct {
	Tie  string `json:"tie"`
	Keep string `json:"keep_a"`
}

type SchemaTieB struct {
	Tie  int    `json:"tie"`
	Keep string `json:"keep_b"`
}

type SchemaTwiceInner struct {
	Twice string `json:"twice"`
}

type SchemaTwiceL struct{ SchemaTwiceInner }

type SchemaTwiceR struct{ SchemaTwiceInner }

// #2716: a tie at one depth drops both fields, whether two structs tag
// one name or one struct is embedded twice. The type is built with
// reflect.StructOf because go vet (rightly) refuses the duplicate tag in
// a literal struct.
func TestJSONFieldsDropTies(t *testing.T) {
	typ := reflect.StructOf([]reflect.StructField{
		{Name: "SchemaTieA", Type: reflect.TypeOf(SchemaTieA{}), Anonymous: true},
		{Name: "SchemaTieB", Type: reflect.TypeOf(SchemaTieB{}), Anonymous: true},
		{Name: "SchemaTwiceL", Type: reflect.TypeOf(SchemaTwiceL{}), Anonymous: true},
		{Name: "SchemaTwiceR", Type: reflect.TypeOf(SchemaTwiceR{}), Anonymous: true},
	})
	var names []string
	for _, f := range jsonFields(typ) {
		names = append(names, f.Name)
	}
	enc, err := json.Marshal(reflect.New(typ).Elem().Interface())
	if err != nil {
		t.Fatal(err)
	}
	if string(enc) != `{"keep_a":"","keep_b":""}` {
		t.Fatalf("encoding/json wrote %s; the premise of this test no longer holds", enc)
	}
	if strings.Join(names, ",") != "keep_a,keep_b" {
		t.Errorf("schema lists %v; encoding/json writes %s", names, enc)
	}
}

func TestSchemaComponentsRefuseEnumOnNonString(t *testing.T) {
	withSchemas(t, []ResponseSchema{{Method: "GET", Path: "/x", Type: reflect.TypeOf(schemaSample{})}},
		[]SchemaEnum{{Schema: "schemaSample", Field: "count", Values: []string{"1"}}})
	if err := writeSchemaComponents(&bytes.Buffer{}); err == nil {
		t.Fatal("want an error for an enum on an integer field")
	}
}

func TestSchemaComponentsRefuseNonStructResponse(t *testing.T) {
	withSchemas(t, []ResponseSchema{{Method: "GET", Path: "/x", Type: reflect.TypeOf([]string{})}}, nil)
	if err := writeSchemaComponents(&bytes.Buffer{}); err == nil {
		t.Fatal("want an error for a slice response type")
	}
}

// The remap review page reads these operations (#2394); each documents
// its 200 body with the reflected schema, not Ack.
func TestRemapOperationsDocumentReflectedSchemas(t *testing.T) {
	var buf bytes.Buffer
	if err := Generate(&buf, RouteSpecs); err != nil {
		t.Fatal(err)
	}
	spec := buf.String()
	for path, schema := range map[string]string{
		"/v1/controls/remap-proposals:\n    get:":                    "RemapProposalsResponse",
		"/v1/controls/remap-proposals/{id}/approve:\n    post:":      "RemapDecision",
		"/v1/admin/crosswalk-edge-remap-proposals:\n    get:":        "EdgeRemapProposalsResponse",
		"/v1/controls/remap-proposals/approve-unchanged:\n    post:": "RemapBulkResult",
		"/v1/controls/remap-proposals/{id}/keep-legacy:\n    post:":  "RemapDecision",
		"/v1/controls/remap-proposals/{id}/reject:\n    post:":       "RemapDecision",
	} {
		i := strings.Index(spec, "  "+path)
		if i < 0 {
			t.Fatalf("operation %q not in spec", path)
		}
		op := spec[i:]
		if j := strings.Index(op[len(path)+2:], "\n  /"); j >= 0 {
			op = op[:len(path)+2+j]
		}
		if !strings.Contains(op, `"200":`) || !strings.Contains(op, `$ref: "#/components/schemas/`+schema+`"`) {
			t.Errorf("%s does not document a 200 %s body:\n%s", path, schema, op)
		}
	}
	for _, want := range []string{
		"    RemapProposal:\n",
		"          enum:\n            - unchanged\n            - renamed\n            - split\n            - reused_id\n            - no_successor\n",
	} {
		if !strings.Contains(spec, want) {
			t.Errorf("spec missing %q", want)
		}
	}
}
