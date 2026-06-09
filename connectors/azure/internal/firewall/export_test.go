package firewall

// MaxRuleCollectionGroupPagesForTest exposes the per-policy ruleCollectionGroups
// page cap (the loop-termination DoS backstop, slice 634) to the external
// _test package so the loop-termination test can assert the cursor walk stops at
// the cap rather than running forever. Test-only; not part of the package API.
func MaxRuleCollectionGroupPagesForTest() int { return maxRuleCollectionGroupPages }
