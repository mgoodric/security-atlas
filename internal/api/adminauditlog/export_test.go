package adminauditlog

import (
	"encoding/json"

	"github.com/mgoodric/security-atlas/internal/audit/unifiedlog"
	"github.com/mgoodric/security-atlas/internal/moduleaccess"
)

// DecisionSeen is entrySeen for an authorization decision with the given
// request path and target type, so the actors query's SQL module filter
// can be checked against the Go one (#2718).
func DecisionSeen(set moduleaccess.ModuleSet, requestPath, targetType string) bool {
	e := unifiedlog.Entry{Kind: unifiedlog.KindDecision, TargetType: targetType}
	if requestPath != "" {
		e.PayloadJSON, _ = json.Marshal(map[string]string{"request_path": requestPath})
	}
	return entrySeen(e, set)
}
