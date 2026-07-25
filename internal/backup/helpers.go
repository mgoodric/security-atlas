package backup

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// discardWriter satisfies io.Writer for a discard slog handler (mirrors the
// other schedulers' nil-logger fallback).
type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

// errString renders an error for a log field without panicking on nil.
func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// boundDetail bounds the detail string written to backup_runs and strips
// control characters / credentials. The reason strings are writer-controlled
// (never echo a raw DSN or credential), but the bound is belt-and-suspenders.
func boundDetail(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		return r
	}, s)
	const max = 500
	if len(s) > max {
		return s[:max]
	}
	return s
}

// fmtUUID renders raw 16 bytes as a canonical UUID string.
func fmtUUID(b [16]byte) string { return uuid.UUID(b).String() }

// parsePositiveInt parses a non-negative integer config value.
func parsePositiveInt(s string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0, err
	}
	if n < 0 {
		return 0, fmt.Errorf("backup: negative value %d", n)
	}
	return n, nil
}

// replaceDBName returns dsn with its database path replaced by name. Handles
// both URL-form DSNs (postgres://host/db?...) and keyword-form DSNs
// (host=... dbname=...). The verifier uses this to derive the maintenance-DB
// and ephemeral-DB connection strings from the migrator DSN.
func replaceDBName(dsn, name string) string {
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		u, err := url.Parse(dsn)
		if err == nil {
			u.Path = "/" + name
			return u.String()
		}
	}
	// keyword form: replace or append dbname=
	if strings.Contains(dsn, "dbname=") {
		parts := strings.Fields(dsn)
		for i, p := range parts {
			if strings.HasPrefix(p, "dbname=") {
				parts[i] = "dbname=" + name
			}
		}
		return strings.Join(parts, " ")
	}
	return strings.TrimSpace(dsn) + " dbname=" + name
}
