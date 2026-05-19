# security-atlas — auditor role policy.
#
# Source attribution: community_draft (slice 035) + slice 025
# (activation: audit-notes write surface + audit-period read).
#
# auditor is the external assessor role. Canvas §9.5 example:
# "auditor X can only see scope cells within audit_period Y for
# client Z." Read-only across audit-relevant resources, with two
# write surfaces (slice 025 + slice 026):
#
#   - audit-notes (slice 025): the auditor's private testing-notes
#     workspace. Period-scoped via audit_period_id; the rego ABAC
#     check below enforces that the note's audit_period_id is one of
#     the auditor's assigned period ids.
#
#   - sample annotations (slice 026): the auditor's findings against
#     a pulled sample. Period-scoped via the samples resource's
#     audit_period_id attribute; rule lives further down + in
#     audit_periods.rego.
#
# ABAC: the audit_period scope check lives in audit_periods.rego so the
# attribute check can be reused for scope_cells. This file establishes
# the role-level allow rules; audit_periods.rego adds the attribute
# guard for the period-scoped resources.
#
# Default-deny is the implicit contract: no `allow if has_role("auditor")
# and action == "write"` exists for any resource OTHER than audit-notes
# (and sample annotations, gated by audit_periods.rego). Mutating
# requests on policies/risks/exceptions/etc fall through default-deny
# and the middleware returns 403 (P0-1 / AC-3).

package authz

# Read access to audit-relevant non-period-scoped resources. The
# auditor does NOT see /v1/admin/* or /v1/risks (those are
# operator-internal).
#
# Period-scoped resources (samples, populations) are NOT in this set --
# they're gated by audit_periods.rego, which adds the ABAC predicate
# check on input.resource.attrs.audit_period_id.
allow if {
    has_role("auditor")
    is_read
    auditor_readable_resources[input.resource.type]
}

# /v1/me/audit-period(s) -- auditor's self-info endpoints (slice 025
# AC-5 + AC-6). The handler reads from auditor_assignments scoped to
# the caller's UserID, so there's no period filter to apply at the
# rego layer -- a row only exists when the caller has an assignment.
allow if {
    has_role("auditor")
    is_read
    input.resource.type == "me"
}

# audit-notes read: the handler enforces author_user_id = caller, so
# cross-author reads return empty. This rego rule only governs whether
# the auditor role can touch the /v1/audit-notes endpoint AT ALL --
# the row-level visibility is enforced at the query layer.
allow if {
    has_role("auditor")
    is_read
    input.resource.type == "audit-notes"
}

# audit-notes write: gated by the period assignment match. The
# request body's audit_period_id is surfaced on
# input.resource.attrs.audit_period_id by the handler before calling
# Decide (slice 025 wiring); the rule denies cross-period writes
# (P0-3 / AC-4).
#
# Slice 029 note: the auditor can write both 'auditor_only' (private)
# and 'shared' (Audit Hub) notes. The period gate is the same for
# both; visibility is enforced at the query layer + handler.
allow if {
    has_role("auditor")
    input.action == "write"
    input.resource.type == "audit-notes"
    auditor_period_matches
}

# Slice 029: /v1/me/notifications -- self-info notification surface.
# Any authenticated role (auditor included) can read their own
# notifications. The query layer pins recipient_user_id = caller.UserID
# so cross-recipient leakage is impossible. Mark-read (PATCH) is
# allowed by the same rule.
allow if {
    has_role("auditor")
    input.resource.type == "notifications"
}

# One write action carried over from the slice-035 stub: annotating
# a sample with audit findings (slice 026:
# POST /v1/samples/{id}/annotations). Period-scoped via
# audit_periods.rego's ABAC predicate.
allow if {
    has_role("auditor")
    input.action == "write"
    input.resource.type == "samples"
    auditor_period_matches
}

auditor_period_matches if {
    some assigned in input.user.attrs.audit_period_ids
    assigned == input.resource.attrs.audit_period_id
}

# Slice 027: auditor can read walkthroughs. The auditor's testing notes
# referencing a walkthrough live in audit_notes with scope_type='walkthrough'
# (slice 029 widened the enum). Visibility=auditor_only keeps those private
# at the query layer; this rule only governs READ on the walkthrough
# artifact itself, which is shared between auditor and control owner per
# AC-4 ("auditor and the control's owner can read").
#
# Auditor write on walkthroughs is intentionally NOT granted at the
# auditor-role layer; walkthroughs can be authored by control_owner or
# grc_engineer (see control_owner.rego + grc_engineer.rego). An auditor
# who needs to record a walkthrough does so via their assigned engineer
# credential or has the grc_engineer role flag set on their credential.
allow if {
    has_role("auditor")
    is_read
    input.resource.type == "walkthroughs"
}

# Resources the auditor can read without an audit_period predicate.
# audit-periods is included so the auditor can list periods (the
# handler-level filter in slice 028 is admin-only -- auditors hit
# /v1/me/audit-period(s) instead, which renders only the assignment-
# scoped view).
#
# Note: scopes is the canvas §5 scope-cell catalog. Slice 035's
# scope_cells.rego adds the cross-cutting ABAC check that auditor
# reads of scope cells must be inside an assigned audit_period.
auditor_readable_resources := {
    "controls",
    "policies",
    "framework-scopes",
    "exceptions",
    "artifacts",
    "scopes",
    "audit-periods",
    # Slice 124: the unified audit-log aggregation endpoint (read-only
    # UNION ALL across the nine per-domain audit-log tables). Auditors
    # need this for the same reason they need 'audit-periods' — visibility
    # into the test artifacts of the program. RLS keeps the read
    # tenant-scoped; the canvas §9.5 ABAC layer is not needed because the
    # log is already tenant-internal (every row already belongs to the
    # caller's tenant after RLS filtering).
    "audit-log-unified",
    # Slice 135: the bulk-download (csv / json / xlsx) variant of the
    # same unified audit-log read. Admit set MUST match audit-log-unified
    # bit-for-bit (slice 135 P0-A9) — the export endpoint is the SAME
    # underlying query with an encoder swap, so any role admitted to
    # the read MUST be admitted to the export. The slice-135 OPA matrix
    # test (TestSlice135_UnifiedAuditLogExportAdmitSetParity) pins this
    # parity at the rego layer.
    "audit-log-export",
    # Slice 148: compliance calendar is cross-business by design
    # (slice 094 AC-9: "accessible to all signed-in users, no admin
    # gate"). The auditor needs visibility into the audit-period
    # milestones and exception expirations that the calendar
    # surfaces. RLS keeps the read tenant-scoped; ABAC narrowing
    # to the auditor's assigned periods is enforced at the query
    # layer (the audit_periods table reads ALL active periods in
    # the tenant — the auditor sees the full program calendar, not
    # just their assigned slice, because v1 has no "assigned
    # auditor sees only their assignments" admit on /v1/calendar
    # — that is a v2 ABAC narrowing if surface demands).
    "calendar",
}

# Slice 148: auditor can mint their own ICS subscription URL via
# POST /v1/calendar/subscription. See viewer.rego for the design
# rationale; the same narrow path predicate keeps the write surface
# bound to the subscription mint.
allow if {
    has_role("auditor")
    input.action == "write"
    input.resource.type == "calendar"
    input.request.path == "/v1/calendar/subscription"
}
