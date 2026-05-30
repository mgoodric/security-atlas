package a

import "net/http"

// writeJSON is a banned package-local declaration.
func writeJSON(w http.ResponseWriter, status int, body any) { // want `slice 369 / H-1: package-local writeJSON declaration duplicates the shared response helper`
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = body
}

// writeError is a banned package-local declaration.
func writeError(w http.ResponseWriter, status int, msg string) { // want `slice 369 / H-1: package-local writeError declaration duplicates the shared response helper`
	writeJSON(w, status, map[string]string{"error": msg})
}

// writeServerErr is a banned package-local declaration.
func writeServerErr(w http.ResponseWriter, r *http.Request, op string, err error) { // want `slice 369 / H-1: package-local writeServerErr declaration duplicates the shared response helper`
	_ = w
	_ = r
	_ = op
	_ = err
}

// writePackError is NOT flagged — it does real sentinel-mapping work and is
// out of scope per P0-369-3. The analyzer only matches the three trivial
// helper names.
func writePackError(w http.ResponseWriter, err error) {
	_ = w
	_ = err
}

type responder struct{}

// writeJSON as a METHOD is NOT flagged — only free functions are the
// duplication surface.
func (responder) writeJSON(w http.ResponseWriter, status int, body any) {
	_ = w
	_ = status
	_ = body
}

// WriteJSON (exported, the shared-helper shape) is NOT flagged.
func WriteJSON(w http.ResponseWriter, status int, body any) {
	_ = w
	_ = status
	_ = body
}
