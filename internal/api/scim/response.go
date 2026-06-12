package scim

import (
	"encoding/json"
	"net/http"

	"github.com/mgoodric/security-atlas/internal/scim"
)

// writeSCIMJSON writes a SCIM resource with the application/scim+json content
// type (RFC 7644 §3.1) and the given status.
func writeSCIMJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", scim.ContentType)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// writeSCIMError writes an RFC 7644 §3.12 SCIM error body. scimType is
// optional — pass "" to omit.
func writeSCIMError(w http.ResponseWriter, status int, scimType, detail string) {
	writeSCIMJSON(w, status, scim.NewError(status, scimType, detail))
}
