package openapi

import (
	"reflect"

	"github.com/mgoodric/security-atlas/internal/api/admincrosswalkremap"
	"github.com/mgoodric/security-atlas/internal/api/controls"
	"github.com/mgoodric/security-atlas/internal/api/scfimport"
	"github.com/mgoodric/security-atlas/internal/control"
	"github.com/mgoodric/security-atlas/internal/crosswalkremap"
)

// ResponseSchemas lists the operations whose 200 body is documented from
// the Go wire struct (#2394). Every other operation keeps the Ack
// placeholder. The SCF remap review page's operations come first; add an
// entry here when a web BFF module moves onto the generated types.
//
// The schema documents every slice as an array, never null, but
// encoding/json writes a nil slice as null. A handler listed here must
// allocate each slice it returns (make([]T, 0), not var s []T), and its
// test should encode a real response and check for null arrays.
var ResponseSchemas = []ResponseSchema{
	{Method: "GET", Path: "/v1/controls/remap-proposals", Type: reflect.TypeOf(controls.RemapProposalsResponse{})},
	{Method: "POST", Path: "/v1/controls/remap-proposals/approve-unchanged", Type: reflect.TypeOf(control.RemapBulkResult{})},
	{Method: "POST", Path: "/v1/controls/remap-proposals/{id}/approve", Type: reflect.TypeOf(control.RemapDecision{})},
	{Method: "POST", Path: "/v1/controls/remap-proposals/{id}/reject", Type: reflect.TypeOf(control.RemapDecision{})},
	{Method: "POST", Path: "/v1/controls/remap-proposals/{id}/keep-legacy", Type: reflect.TypeOf(control.RemapDecision{})},
	{Method: "GET", Path: "/v1/admin/crosswalk-edge-remap-proposals", Type: reflect.TypeOf(admincrosswalkremap.ListResponse{})},
}

// schemaNames renames a Go type whose own name is too generic for the
// shared components namespace.
var schemaNames = map[reflect.Type]string{
	reflect.TypeOf(admincrosswalkremap.ListResponse{}): "EdgeRemapProposalsResponse",
	reflect.TypeOf(crosswalkremap.Proposal{}):          "EdgeRemapProposal",
}

func remapClassValues() []string {
	out := make([]string, len(scfimport.RemapClasses))
	for i, c := range scfimport.RemapClasses {
		out[i] = string(c)
	}
	return out
}

// SchemaEnums narrows string fields to the values the Go source lists.
var SchemaEnums = []SchemaEnum{
	{Schema: "RemapProposal", Field: "class", Values: remapClassValues()},
	{Schema: "RemapProposal", Field: "status", Values: control.RemapStatuses},
	{Schema: "EdgeRemapProposal", Field: "class", Values: remapClassValues()},
	{Schema: "EdgeRemapProposal", Field: "status", Values: crosswalkremap.Statuses},
}
