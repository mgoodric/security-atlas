// Slice 140 — minimal OpenAPI 3.1 shape validator.
//
// Validates that the generated `docs/openapi.yaml` is structurally
// well-formed AND conforms to the load-bearing OpenAPI 3.1 invariants
// the generator owns. Used by:
//
//   - The Go unit test `TestSpecValidatesAgainstShape` in
//     `validator_test.go` — runs on every `go test ./...`.
//   - The slice 140 AC-8 informational CI check — surfaces any
//     malformation as a sticky comment.
//
// This is NOT a full OpenAPI 3.1 spec validator (that would require
// pulling in `kin-openapi/openapi3`, ~80 KB of new dep surface, plus
// its transitive load). Instead it asserts the structural properties
// that, if violated, would either break Redoc's render OR contradict
// the load-bearing P0 anti-criteria.
//
// What it checks:
//
//   - Parses as valid YAML.
//   - Top-level `openapi: 3.x.x`.
//   - `info.title` + `info.version` present.
//   - At least one entry under `paths:`.
//   - Every operation under every path has `summary`, `tags`,
//     `operationId`, `responses`.
//   - Every security reference (`security: [{<name>: []}]`) names a
//     scheme declared under `components.securitySchemes`.
//
// What it does NOT check (out of scope for this validator):
//
//   - Per-operation request/response body schemas (v1 spec uses the
//     Ack envelope as a placeholder — a follow-on slice refines).
//   - Example values (the v1 spec emits no examples).
//   - JSON Schema draft 2020-12 conformance of any inline schema.

package openapi

import (
	"fmt"
	"sort"

	"gopkg.in/yaml.v3"
)

// Validate parses the YAML spec at the given bytes and returns the
// list of violations. An empty slice means the spec is valid for the
// purposes of this validator.
//
// Returns a hard error only when the input is not parseable YAML —
// every other failure mode surfaces as a violation in the returned
// slice so callers can render them all at once.
func Validate(spec []byte) ([]string, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(spec, &root); err != nil {
		return nil, fmt.Errorf("spec is not valid YAML: %w", err)
	}
	// yaml.Unmarshal returns a Document node whose first content is
	// the mapping we want.
	if root.Kind != yaml.DocumentNode || len(root.Content) != 1 {
		return nil, fmt.Errorf("spec root is not a single document mapping")
	}
	top := root.Content[0]
	if top.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("spec top-level is not a mapping")
	}

	violations := []string{}
	addViolation := func(format string, args ...any) {
		violations = append(violations, fmt.Sprintf(format, args...))
	}

	topMap := mappingToMap(top)

	// openapi: 3.x.x
	if v, ok := topMap["openapi"]; !ok || v.Value == "" {
		addViolation("top-level `openapi` field missing")
	} else if v.Value[:2] != "3." {
		addViolation("top-level `openapi` is %q; expected `3.x.x`", v.Value)
	}

	// info block
	if info, ok := topMap["info"]; !ok || info.Kind != yaml.MappingNode {
		addViolation("top-level `info` block missing or not a mapping")
	} else {
		infoMap := mappingToMap(info)
		if v, ok := infoMap["title"]; !ok || v.Value == "" {
			addViolation("`info.title` missing")
		}
		if v, ok := infoMap["version"]; !ok || v.Value == "" {
			addViolation("`info.version` missing")
		}
	}

	// paths block — must exist and have at least one entry.
	paths, ok := topMap["paths"]
	if !ok || paths.Kind != yaml.MappingNode {
		addViolation("top-level `paths` block missing or not a mapping")
		return violations, nil
	}
	if len(paths.Content) == 0 {
		addViolation("`paths` block is empty")
	}

	// components.securitySchemes — collect the declared scheme names.
	declaredSchemes := map[string]struct{}{}
	if components, ok := topMap["components"]; ok && components.Kind == yaml.MappingNode {
		compMap := mappingToMap(components)
		if schemes, ok := compMap["securitySchemes"]; ok && schemes.Kind == yaml.MappingNode {
			for i := 0; i < len(schemes.Content); i += 2 {
				declaredSchemes[schemes.Content[i].Value] = struct{}{}
			}
		}
	}

	// Walk every operation under every path.
	for i := 0; i < len(paths.Content); i += 2 {
		pathName := paths.Content[i].Value
		pathItem := paths.Content[i+1]
		if pathItem.Kind != yaml.MappingNode {
			addViolation("path %q is not a mapping", pathName)
			continue
		}
		for j := 0; j < len(pathItem.Content); j += 2 {
			method := pathItem.Content[j].Value
			op := pathItem.Content[j+1]
			if op.Kind != yaml.MappingNode {
				addViolation("operation %s %s is not a mapping", method, pathName)
				continue
			}
			opMap := mappingToMap(op)
			for _, req := range []string{"summary", "tags", "operationId", "responses"} {
				if _, ok := opMap[req]; !ok {
					addViolation("operation %s %s missing required field %q", method, pathName, req)
				}
			}
			// Verify referenced security schemes exist.
			if sec, ok := opMap["security"]; ok && sec.Kind == yaml.SequenceNode {
				for _, entry := range sec.Content {
					if entry.Kind != yaml.MappingNode {
						continue
					}
					for k := 0; k < len(entry.Content); k += 2 {
						schemeName := entry.Content[k].Value
						if _, ok := declaredSchemes[schemeName]; !ok {
							addViolation("operation %s %s references undeclared security scheme %q",
								method, pathName, schemeName)
						}
					}
				}
			}
		}
	}

	sort.Strings(violations)
	return violations, nil
}

// mappingToMap collapses an even-content mapping node into a Go map
// for keyed lookup. Returns an empty map for non-mapping inputs.
func mappingToMap(n *yaml.Node) map[string]*yaml.Node {
	out := map[string]*yaml.Node{}
	if n.Kind != yaml.MappingNode {
		return out
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		out[n.Content[i].Value] = n.Content[i+1]
	}
	return out
}
