package adminusers

// SetBeforeRevokeCommitForTest installs a hook that Revoke runs after its
// last-admin check and before it commits (#2261).
func (h *Handler) SetBeforeRevokeCommitForTest(fn func()) *Handler {
	h.beforeRevokeCommit = fn
	return h
}
