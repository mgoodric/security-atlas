// The organisation's own risk ID (#2838): "R-014", kept on the risk so a
// security leader finds risks by the IDs their team already uses, and so
// the risk register import (#2812) has a duplicate key and a parent-link
// key. It is optional and unique within the tenant when set (migration
// 20261278000000, index risks_tenant_external_ref_key).

package risk

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgconn"
)

// MaxExternalRefLen is the longest organisation risk ID, in characters;
// the risks_external_ref_shape CHECK holds the same bound.
const MaxExternalRefLen = 128

// externalRefIndex is the partial unique index on (tenant_id, external_ref).
const externalRefIndex = "risks_tenant_external_ref_key"

// ErrExternalRefTaken is returned when another risk of the tenant already
// carries the organisation risk ID. The HTTP layer renders it as a 409.
var ErrExternalRefTaken = errors.New("risk: external_ref is already used by another risk in this tenant")

// NormalizeExternalRef trims an organisation risk ID. An empty result is
// unset (nil). One longer than MaxExternalRefLen is ErrRecordFieldInvalid.
func NormalizeExternalRef(raw string) (*string, error) {
	ref := strings.TrimSpace(raw)
	if ref == "" {
		return nil, nil
	}
	if utf8.RuneCountInString(ref) > MaxExternalRefLen {
		return nil, fmt.Errorf("%w: external_ref is longer than %d characters", ErrRecordFieldInvalid, MaxExternalRefLen)
	}
	return &ref, nil
}

// mapExternalRefErr turns a unique violation on the organisation risk ID
// into ErrExternalRefTaken and wraps any other error with op.
func mapExternalRefErr(op string, err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == externalRefIndex {
		return ErrExternalRefTaken
	}
	return fmt.Errorf("%s: %w", op, err)
}

func sameRef(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// refValue is an organisation risk ID as the audit row holds it: the ID,
// or null.
func refValue(r *string) any {
	if r == nil {
		return nil
	}
	return *r
}
