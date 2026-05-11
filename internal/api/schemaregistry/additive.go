package schemaregistry

import (
	"encoding/json"
	"fmt"
	"sort"
)

// CheckAdditiveOver verifies that `next` is a strict superset of `prev` in
// the senses that AC-5 demands for an additive minor bump:
//
//  1. Every property required by `prev` is still required by `next`.
//  2. No property that existed in `prev` is removed from `next` (and no
//     existing property's type changes).
//  3. `additionalProperties` does not become more restrictive (false ->
//     true is allowed; true -> false is rejected; absent stays absent).
//
// Anything more permissive — adding new optional properties, loosening
// formats, adding enum values — is fine. The check is intentionally
// shallow: it inspects the top-level `properties`, `required`, and
// `additionalProperties` keys. Deep-nested schemas can graduate to a
// dedicated diff in a later slice; the four most common minor-bump
// regressions live at the top level.
//
// prev and next are JSON-encoded schemas. The function returns nil when
// `next` is additive over `prev`; an error describing the first
// non-additive change otherwise.
func CheckAdditiveOver(prev, next []byte) error {
	var pSchema, nSchema map[string]any
	if err := json.Unmarshal(prev, &pSchema); err != nil {
		return fmt.Errorf("decode prev schema: %w", err)
	}
	if err := json.Unmarshal(next, &nSchema); err != nil {
		return fmt.Errorf("decode next schema: %w", err)
	}

	// 1. required
	pReq := requiredSet(pSchema)
	nReq := requiredSet(nSchema)
	missing := make([]string, 0)
	for r := range pReq {
		if !nReq[r] {
			missing = append(missing, r)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		return fmt.Errorf("additive check failed: required field(s) %v removed", missing)
	}

	// 2. properties — no removed property; no type change for an existing one.
	pProps := propsMap(pSchema)
	nProps := propsMap(nSchema)
	removed := make([]string, 0)
	for name := range pProps {
		if _, ok := nProps[name]; !ok {
			removed = append(removed, name)
		}
	}
	sort.Strings(removed)
	if len(removed) > 0 {
		return fmt.Errorf("additive check failed: property(ies) %v removed", removed)
	}
	for name, prevSchema := range pProps {
		nextSchema, ok := nProps[name]
		if !ok {
			continue
		}
		pt := typeOf(prevSchema)
		nt := typeOf(nextSchema)
		if pt != "" && nt != "" && pt != nt {
			return fmt.Errorf("additive check failed: property %q type changed from %q to %q", name, pt, nt)
		}
	}

	// 3. additionalProperties tightening. We only catch the explicit
	// true -> false transition; the absent-in-next case (treated as the
	// default `true`) is more permissive than `false` and accepted.
	if pAP, ok := pSchema["additionalProperties"]; ok {
		if nAP, nOK := nSchema["additionalProperties"]; nOK {
			pAPBool, pIsBool := pAP.(bool)
			nAPBool, nIsBool := nAP.(bool)
			if pIsBool && nIsBool && pAPBool && !nAPBool {
				return fmt.Errorf("additive check failed: additionalProperties tightened (true -> false)")
			}
		}
	}

	return nil
}

func requiredSet(schema map[string]any) map[string]bool {
	out := map[string]bool{}
	raw, ok := schema["required"].([]any)
	if !ok {
		return out
	}
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out[s] = true
		}
	}
	return out
}

func propsMap(schema map[string]any) map[string]map[string]any {
	out := map[string]map[string]any{}
	raw, ok := schema["properties"].(map[string]any)
	if !ok {
		return out
	}
	for name, sub := range raw {
		if m, ok := sub.(map[string]any); ok {
			out[name] = m
		}
	}
	return out
}

func typeOf(schema map[string]any) string {
	t, _ := schema["type"].(string)
	return t
}
