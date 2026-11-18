package openapi

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
	"time"
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
