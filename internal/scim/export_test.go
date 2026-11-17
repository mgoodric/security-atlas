package scim

import (
	"context"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// WithNotifierForTest replaces the super-admin notifier, so a test can make
// the last-admin alert fail (#2504).
func (s *Store) WithNotifierForTest(f func(context.Context, dbx.DBTX, dbx.NotifySuperAdminsLastAdminDeprovisionedParams) (int32, error)) *Store {
	s.notify = f
	return s
}
