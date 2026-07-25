package backup

import (
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// quoteIdent double-quotes a Postgres identifier (table/column name) and
// escapes embedded double-quotes. Idents come from pg_catalog, never from
// user input, but quoting is correct regardless.
func quoteIdent(id string) string {
	return `"` + strings.ReplaceAll(id, `"`, `""`) + `"`
}

// quoteString single-quotes a string literal, escaping embedded quotes and
// backslashes (E” escape form for backslash safety).
func quoteString(s string) string {
	if strings.ContainsRune(s, '\\') {
		return "E'" + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `'`, `''`) + "'"
	}
	return "'" + strings.ReplaceAll(s, `'`, `''`) + "'"
}

// sqlLiteralForType renders a pgx-decoded value as a Postgres SQL literal,
// disambiguated by the column's declared type. pgx decodes BOTH a jsonb column
// and a text[] column to []any, so the value alone cannot tell them apart —
// the column type does. Array columns (type ending in "[]") render as a
// Postgres array literal cast to the column type; everything else falls
// through to the value-shape renderer.
func sqlLiteralForType(v any, dataType string) string {
	if v == nil {
		return "NULL"
	}
	if strings.HasSuffix(dataType, "[]") {
		return arrayLiteral(v, dataType)
	}
	return sqlLiteral(v)
}

// arrayLiteral renders a decoded array value ([]any of scalars) as a Postgres
// ARRAY[...] literal cast to the column's declared array type. Handles the
// element shapes the schema's array columns use (text[], uuid[]).
func arrayLiteral(v any, dataType string) string {
	elems, ok := v.([]any)
	if !ok {
		// Unexpected shape; fall back to the value renderer (cast to type).
		return sqlLiteral(v) + "::" + dataType
	}
	if len(elems) == 0 {
		return "ARRAY[]::" + dataType
	}
	parts := make([]string, len(elems))
	for i, e := range elems {
		parts[i] = sqlLiteral(e)
	}
	return "ARRAY[" + strings.Join(parts, ", ") + "]::" + dataType
}

// sqlLiteral renders a pgx-decoded value as a Postgres SQL literal suitable
// for an INSERT VALUES tuple. It handles the value shapes pgx.Rows.Values()
// returns for the column types this schema uses (uuid, text, timestamptz,
// numeric, jsonb, bytea, bool, ints, arrays).
func sqlLiteral(v any) string {
	switch t := v.(type) {
	case nil:
		return "NULL"
	case bool:
		if t {
			return "true"
		}
		return "false"
	case string:
		return quoteString(t)
	case int16:
		return strconv.FormatInt(int64(t), 10)
	case int32:
		return strconv.FormatInt(int64(t), 10)
	case int64:
		return strconv.FormatInt(t, 10)
	case float32:
		return strconv.FormatFloat(float64(t), 'g', -1, 32)
	case float64:
		return strconv.FormatFloat(t, 'g', -1, 64)
	case []byte:
		// bytea — hex escape form, the portable bytea literal.
		return "'\\x" + hex.EncodeToString(t) + "'::bytea"
	case time.Time:
		return quoteString(t.UTC().Format("2006-01-02 15:04:05.999999-07:00")) + "::timestamptz"
	case uuid.UUID:
		return quoteString(t.String()) + "::uuid"
	case [16]byte:
		return quoteString(uuid.UUID(t).String()) + "::uuid"
	case pgtype.UUID:
		if !t.Valid {
			return "NULL"
		}
		return quoteString(uuid.UUID(t.Bytes).String()) + "::uuid"
	case map[string]any:
		return jsonLiteral(t)
	case []any:
		return jsonLiteral(t)
	case pgtype.Numeric:
		return numericLiteral(t)
	default:
		// Fallback: stringify and quote. Covers driver-specific wrappers
		// not enumerated above; correctness is bounded by the
		// restore-verification replay (a bad literal fails the cycle loudly).
		return quoteString(fmt.Sprintf("%v", t))
	}
}

// jsonLiteral renders a decoded JSON value (map/slice) back to a jsonb
// literal. pgx decodes jsonb columns to map[string]any / []any.
func jsonLiteral(v any) string {
	b, err := marshalCanonicalJSON(v)
	if err != nil {
		return "NULL"
	}
	return quoteString(string(b)) + "::jsonb"
}

func numericLiteral(n pgtype.Numeric) string {
	if !n.Valid {
		return "NULL"
	}
	val, err := n.Value()
	if err != nil || val == nil {
		return "NULL"
	}
	s, ok := val.(string)
	if !ok {
		return "NULL"
	}
	return s
}
