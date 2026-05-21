package revocation

import "github.com/jackc/pgx/v5"

// pgxNoRowsValue exposes pgx.ErrNoRows as a package-local sentinel
// reachable from revocation.go's pgxNoRows() helper without importing
// pgx at the top of the main file. Splitting the import out keeps
// the public surface of revocation.go free of driver leakage.
func pgxNoRowsValue() error {
	return pgx.ErrNoRows
}
