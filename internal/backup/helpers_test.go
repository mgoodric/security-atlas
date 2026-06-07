package backup

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestHashBytes(t *testing.T) {
	t.Parallel()
	// Known sha256 of "abc".
	const want = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	if got := HashBytes([]byte("abc")); got != want {
		t.Fatalf("HashBytes(abc) = %s, want %s", got, want)
	}
	if HashBytes([]byte("abc")) == HashBytes([]byte("abd")) {
		t.Fatal("distinct inputs hashed identically")
	}
}

func TestArtifactNameRoundTrip(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 6, 7, 3, 4, 5, 0, time.UTC)
	name := ArtifactName(at)
	if name != "atlas-backup-20260607T030405Z.sql" {
		t.Fatalf("ArtifactName = %q", name)
	}
	got, ok := parseArtifactTime(name)
	if !ok {
		t.Fatalf("parseArtifactTime(%q) failed", name)
	}
	if !got.Equal(at) {
		t.Fatalf("round-trip time = %v, want %v", got, at)
	}
}

func TestParseArtifactTimeRejectsNonBackup(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"random.txt", "atlas-backup-bad.sql", "atlas-backup-20260607T030405Z.sha256", ".env"} {
		if _, ok := parseArtifactTime(name); ok {
			t.Errorf("parseArtifactTime(%q) = true, want false", name)
		}
	}
}

func TestSelectForDeletion(t *testing.T) {
	t.Parallel()
	// Build 10 daily backups, one per day, newest = day 0.
	base := time.Date(2026, 6, 7, 2, 0, 0, 0, time.UTC)
	var objs []BackupObject
	for i := 0; i < 10; i++ {
		ts := base.AddDate(0, 0, -i)
		objs = append(objs, BackupObject{Name: ArtifactName(ts), ModTime: ts})
	}

	t.Run("keeps_daily_window_plus_weekly", func(t *testing.T) {
		t.Parallel()
		// keepDaily=3, keepWeekly=2. The 3 newest days are kept; the weekly
		// rule additionally keeps the newest backup of the two most-recent
		// ISO weeks (which overlap the daily-kept set near the top, then
		// reach one older week). So total kept >= 3, and unbounded growth is
		// prevented: some of the 10 are deleted.
		del := SelectForDeletion(objs, 3, 2)
		if len(del) == 0 {
			t.Fatal("expected some deletions for a 10-backup set with keepDaily=3 keepWeekly=2")
		}
		if len(del) >= len(objs) {
			t.Fatalf("deleted all %d backups; window must retain some", len(objs))
		}
		// The newest backup must NEVER be deleted.
		newest := ArtifactName(base)
		for _, d := range del {
			if d == newest {
				t.Fatalf("rotation deleted the newest backup %q", newest)
			}
		}
	})

	t.Run("keep_all_when_window_exceeds_count", func(t *testing.T) {
		t.Parallel()
		if del := SelectForDeletion(objs, 100, 100); len(del) != 0 {
			t.Fatalf("expected no deletions when window > count, got %v", del)
		}
	})

	t.Run("ignores_unrelated_files", func(t *testing.T) {
		t.Parallel()
		mixed := append([]BackupObject{{Name: "notes.txt"}, {Name: ".env"}}, objs...)
		del := SelectForDeletion(mixed, 1, 1)
		for _, d := range del {
			if d == "notes.txt" || d == ".env" {
				t.Fatalf("rotation selected unrelated file %q for deletion", d)
			}
		}
	})

	t.Run("zero_window_deletes_all_dated", func(t *testing.T) {
		t.Parallel()
		del := SelectForDeletion(objs, 0, 0)
		if len(del) != len(objs) {
			t.Fatalf("keepDaily=0 keepWeekly=0 should delete all %d dated backups, got %d", len(objs), len(del))
		}
	})
}

func TestConfigFromLookupDefaults(t *testing.T) {
	t.Parallel()
	cfg := configFromLookup(func(string) (string, bool) { return "", false })
	if cfg.TargetKind != "local" {
		t.Errorf("TargetKind default = %q, want local", cfg.TargetKind)
	}
	if cfg.Dir != DefaultDir {
		t.Errorf("Dir default = %q, want %q", cfg.Dir, DefaultDir)
	}
	if cfg.Interval != DefaultInterval || cfg.VerifyInterval != DefaultVerifyInterval {
		t.Errorf("interval defaults wrong: %v / %v", cfg.Interval, cfg.VerifyInterval)
	}
	if cfg.KeepDaily != DefaultKeepDaily || cfg.KeepWeekly != DefaultKeepWeekly {
		t.Errorf("retention defaults wrong: %d / %d", cfg.KeepDaily, cfg.KeepWeekly)
	}
	if cfg.MaintenanceDB != DefaultMaintenanceDB {
		t.Errorf("MaintenanceDB default = %q", cfg.MaintenanceDB)
	}
}

func TestConfigFromLookupOverrides(t *testing.T) {
	t.Parallel()
	env := map[string]string{
		envTargetKind:        "s3",
		envBackupDir:         "/data/backups",
		envS3Bucket:          "my-bucket",
		envS3Prefix:          "atlas/",
		envBackupInterval:    "6h",
		envVerifyInterval:    "12h",
		envKeepDaily:         "14",
		envKeepWeekly:        "8",
		envVerifyMaintenance: "template1",
	}
	cfg := configFromLookup(func(k string) (string, bool) { v, ok := env[k]; return v, ok })
	if cfg.TargetKind != "s3" || cfg.S3Bucket != "my-bucket" || cfg.S3Prefix != "atlas/" {
		t.Errorf("s3 config not applied: %+v", cfg)
	}
	if cfg.Interval != 6*time.Hour || cfg.VerifyInterval != 12*time.Hour {
		t.Errorf("interval overrides wrong: %v / %v", cfg.Interval, cfg.VerifyInterval)
	}
	if cfg.KeepDaily != 14 || cfg.KeepWeekly != 8 {
		t.Errorf("retention overrides wrong: %d / %d", cfg.KeepDaily, cfg.KeepWeekly)
	}
	if cfg.MaintenanceDB != "template1" {
		t.Errorf("MaintenanceDB override = %q", cfg.MaintenanceDB)
	}
}

func TestConfigRejectsInvalid(t *testing.T) {
	t.Parallel()
	env := map[string]string{
		envTargetKind:     "ftp",       // invalid -> keep default
		envBackupInterval: "not-a-dur", // invalid -> keep default
		envKeepDaily:      "-3",        // negative -> keep default
	}
	cfg := configFromLookup(func(k string) (string, bool) { v, ok := env[k]; return v, ok })
	if cfg.TargetKind != "local" {
		t.Errorf("invalid target kind should fall back to local, got %q", cfg.TargetKind)
	}
	if cfg.Interval != DefaultInterval {
		t.Errorf("invalid interval should fall back to default, got %v", cfg.Interval)
	}
	if cfg.KeepDaily != DefaultKeepDaily {
		t.Errorf("negative keepDaily should fall back to default, got %d", cfg.KeepDaily)
	}
}

func TestValidateName(t *testing.T) {
	t.Parallel()
	good := []string{"atlas-backup-20260607T030405Z.sql", "a.sql"}
	bad := []string{"", "../etc/passwd", "a/b", "a\\b", "..", ".", "x..y"}
	for _, n := range good {
		if err := validateName(n); err != nil {
			t.Errorf("validateName(%q) = %v, want nil", n, err)
		}
	}
	for _, n := range bad {
		if err := validateName(n); err == nil {
			t.Errorf("validateName(%q) = nil, want error", n)
		}
	}
}

func TestReplaceDBName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		dsn, name, want string
	}{
		{"postgres://u:p@host:5432/atlas?sslmode=disable", "postgres", "postgres://u:p@host:5432/postgres?sslmode=disable"},
		{"postgresql://host/security_atlas", "ephemeral_x", "postgresql://host/ephemeral_x"},
		{"host=localhost dbname=atlas user=atlas_migrate", "postgres", "host=localhost dbname=postgres user=atlas_migrate"},
		{"host=localhost user=atlas_migrate", "newdb", "host=localhost user=atlas_migrate dbname=newdb"},
	}
	for _, c := range cases {
		if got := replaceDBName(c.dsn, c.name); got != c.want {
			t.Errorf("replaceDBName(%q, %q) = %q, want %q", c.dsn, c.name, got, c.want)
		}
	}
}

func TestSQLLiteral(t *testing.T) {
	t.Parallel()
	id := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	cases := []struct {
		in   any
		want string
	}{
		{nil, "NULL"},
		{true, "true"},
		{false, "false"},
		{"o'brien", "'o''brien'"},
		{`back\slash`, `E'back\\slash'`},
		{int64(42), "42"},
		{int32(7), "7"},
		{[]byte{0xde, 0xad}, "'\\xdead'::bytea"},
		{id, "'11111111-1111-4111-8111-111111111111'::uuid"},
		{pgtype.UUID{Bytes: id, Valid: true}, "'11111111-1111-4111-8111-111111111111'::uuid"},
		{pgtype.UUID{Valid: false}, "NULL"},
		{map[string]any{"k": "v"}, `'{"k":"v"}'::jsonb`},
	}
	for _, c := range cases {
		if got := sqlLiteral(c.in); got != c.want {
			t.Errorf("sqlLiteral(%#v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestBoundDetail(t *testing.T) {
	t.Parallel()
	if got := boundDetail("line1\nline2\ttab"); got != "line1 line2 tab" {
		t.Errorf("boundDetail stripped wrong: %q", got)
	}
	long := make([]byte, 600)
	for i := range long {
		long[i] = 'x'
	}
	if got := boundDetail(string(long)); len(got) != 500 {
		t.Errorf("boundDetail length = %d, want 500", len(got))
	}
}
