package demoseed

import "strings"

// Stock bootstrap seed: the organisation data a fresh single-tenant install
// holds before anyone has used it (#1765). ApplyInPlace accepts a tenant only
// when every control and policy it holds is stock.

// stockPolicyCreatedBy is the created_by stamp internal/policy/seed writes on
// the stock policies (`atlas-cli policies seed-stock`).
const stockPolicyCreatedBy = "system:stock-seed"

// isTenantAuditTable reports whether a tenant-scoped table is an audit log:
// `*_audit_log`, `*_audit`, or one of auditTrailLogTables. Matt's call on
// #2039 (M2): the in-place seed never records audit-log rows, so teardown
// leaves them as a trace.
func isTenantAuditTable(name string) bool {
	return strings.HasSuffix(name, "_audit_log") || strings.HasSuffix(name, "_audit") ||
		auditTrailLogTables[name]
}

// auditTrailLogTables are the tenant-scoped audit trails whose names miss the
// `_audit` suffixes (#2043 D3). The other tenant-scoped `*_log` tables are not
// audit trails and stay ordinary tables: channel_delivery_log and
// email_delivery_log record notification sends, and staleness_rollup_log is an
// evaluation read model.
var auditTrailLogTables = map[string]bool{
	// A source of the unified audit log (internal/audit/unifiedlog, #1786).
	"framework_scope_workflow_log": true,
	// Who downloaded which evidence artifact: an access trail.
	"artifact_access_log": true,
}
