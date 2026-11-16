- **me_audit_log.action CHECK:** extended to admit
  `controls_history_export` (slice 175). The down migration DELETEs
  `controls_history_export` rows before restoring the prior CHECK —
  destructive in a prod rollback context; operators wanting to
  preserve forensics must archive these rows separately before
  applying the down (slice 137 D5 precedent).
