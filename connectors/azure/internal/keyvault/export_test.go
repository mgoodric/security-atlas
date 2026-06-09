package keyvault

// MaxRoleAssignmentPagesForTest exposes the per-vault roleAssignments nextLink
// page cap so the loop-termination DoS-backstop test can assert the self-pointing
// cursor terminates at the cap (slice 623). Mirrors firewall's
// MaxRuleCollectionGroupPagesForTest.
func MaxRoleAssignmentPagesForTest() int { return maxRoleAssignmentPages }
