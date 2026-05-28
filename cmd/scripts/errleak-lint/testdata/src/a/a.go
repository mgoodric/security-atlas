package a

import (
	"errors"
	"net/http"
)

func writeJSON(w http.ResponseWriter, code int, body any)    {}
func writeError(w http.ResponseWriter, code int, msg string) {}

// LeakAt5xxWriteJSON triggers the analyzer.
func LeakAt5xxWriteJSON(w http.ResponseWriter) {
	err := errors.New("x")
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "op: " + err.Error()}) // want `slice 367 / CWE-209: writeJSON at 5xx status reflects err.Error\(\)`
}

// LeakAt5xxWriteError triggers the analyzer.
func LeakAt5xxWriteError(w http.ResponseWriter) {
	err := errors.New("x")
	writeError(w, http.StatusInternalServerError, err.Error()) // want `slice 367 / CWE-209: writeError at 5xx status reflects err.Error\(\)`
}

// LeakAt502BadGateway triggers the analyzer (any 5xx).
func LeakAt502BadGateway(w http.ResponseWriter) {
	err := errors.New("x")
	writeError(w, http.StatusBadGateway, "discovery: "+err.Error()) // want `slice 367 / CWE-209: writeError at 5xx status reflects err.Error\(\)`
}

// AcceptableAt4xx is NOT flagged — 4xx with err.Error() is allowed
// per slice 367 D1 (out of scope for v1 cleanup).
func AcceptableAt4xx(w http.ResponseWriter) {
	err := errors.New("x")
	writeError(w, http.StatusBadRequest, err.Error())
}

// AcceptableAt4xxConflict is NOT flagged.
func AcceptableAt4xxConflict(w http.ResponseWriter) {
	err := errors.New("x")
	writeError(w, http.StatusConflict, err.Error())
}

// AcceptableGenericMessage is NOT flagged — no err.Error() at the call.
func AcceptableGenericMessage(w http.ResponseWriter) {
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
}

// AcceptableSlogCall is NOT flagged — slog.Error is not a write helper.
// (Implicit — slog isn't in writeFnNames; left here as a documentation
// fixture for future contributors.)
