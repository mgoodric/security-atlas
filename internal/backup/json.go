package backup

import "encoding/json"

// marshalCanonicalJSON serializes a decoded JSON value. encoding/json sorts
// map keys, giving a deterministic rendering — desirable so a backup of an
// unchanged DB hashes identically run-to-run (helps operators spot drift).
func marshalCanonicalJSON(v any) ([]byte, error) {
	return json.Marshal(v)
}
