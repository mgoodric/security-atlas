- **super_admin_audit_log.action CHECK + me_audit_log.action CHECK:**
  both extended to admit two new values (slice 205):
  `demo_seed_apply` and `demo_seed_teardown`. The seeder writes one
  super_admin_audit_log row + one me_audit_log row per
  apply/teardown event so the operator's forensic trail records demo
  activity at the same level as super_admin grant/revoke events.
