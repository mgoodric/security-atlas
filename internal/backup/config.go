// Package backup is the slice 510 automated-backup + scheduled
// restore-verification feature. It operationalizes the slice-432 manual
// runbook and lets a solo self-host operator meet the slice-373 BCP/DR
// RTO/RPO tiers without depending on manual diligence.
//
// Shape:
//
//	Target     — a pluggable backup destination (Put/List/Get/Delete).
//	             LocalTarget (default, single-VM volume) + S3Target
//	             (off-host durability). One interface, two impls (D3).
//	Dumper     — a pure-Go logical Postgres dump (D2: no pg_dump shell-out;
//	             the runtime is distroless/static with no shell).
//	Backuper   — produces a dump, hashes it (sha256, D7), writes it through
//	             a Target, rotates old backups (D5), records a backup_runs
//	             status row (D8).
//	Verifier   — restores the latest backup into an EPHEMERAL throwaway DB,
//	             recomputes+checks the hash BEFORE replay (D7/AC-5), runs a
//	             smoke check, then DROPs the ephemeral DB (D6/P0-510-2).
//	Scheduler  — two in-process tick-loops (D1) mirroring the
//	             exception-expiry / metrics schedulers; no external cron.
//
// SECURITY (threat model, slice doc):
//
//   - A backup is a FULL cross-tenant copy — it deliberately crosses the RLS
//     boundary (#6). Containment: it is a DEPLOYMENT-privileged operation
//     (runs as the BYPASSRLS migrator role), unreachable from any tenant API.
//     backup_runs is granted to atlas_migrate ONLY (P0-510-1 / AC-7).
//   - Every backup carries a sha256 verified before any restore (AC-5).
//   - The ephemeral verification DB is torn down even on failure (P0-510-2).
//   - Retention/rotation bounds disk growth (P0-510-3).
package backup

import (
	"os"
	"time"
)

// Environment variable names, prefixed ATLAS_BACKUP_ per the platform
// ATLAS_* config convention (mirrors internal/notify/email + internal/llm).
const (
	envBackupDir         = "ATLAS_BACKUP_DIR"
	envBackupInterval    = "ATLAS_BACKUP_INTERVAL"
	envVerifyInterval    = "ATLAS_BACKUP_VERIFY_INTERVAL"
	envKeepDaily         = "ATLAS_BACKUP_KEEP_DAILY"
	envKeepWeekly        = "ATLAS_BACKUP_KEEP_WEEKLY"
	envTargetKind        = "ATLAS_BACKUP_TARGET"    // local | s3
	envS3Bucket          = "ATLAS_BACKUP_S3_BUCKET" // s3 target bucket
	envS3Prefix          = "ATLAS_BACKUP_S3_PREFIX" // key prefix within bucket
	envVerifyMaintenance = "ATLAS_BACKUP_VERIFY_MAINTENANCE_DB"
)

// Defaults (D4 daily cadence, D5 retention window).
const (
	DefaultInterval       = 24 * time.Hour
	DefaultVerifyInterval = 24 * time.Hour
	DefaultKeepDaily      = 7
	DefaultKeepWeekly     = 4
	// DefaultMaintenanceDB is the cluster database the verifier connects to
	// in order to CREATE/DROP the ephemeral verification database (D6). The
	// standard Postgres maintenance database.
	DefaultMaintenanceDB = "postgres"
)

// Config is the env-driven backup configuration for self-host (D1/D4/D5).
type Config struct {
	// TargetKind is "local" (default) or "s3".
	TargetKind string
	// Dir is the local-target directory (default ATLAS_BACKUP_DIR or
	// /var/lib/atlas/backups). A mounted named volume on the single-VM host.
	Dir string
	// S3Bucket / S3Prefix wire the off-host S3 target.
	S3Bucket string
	S3Prefix string
	// Interval / VerifyInterval are the two scheduler cadences.
	Interval       time.Duration
	VerifyInterval time.Duration
	// KeepDaily / KeepWeekly bound retention (P0-510-3).
	KeepDaily  int
	KeepWeekly int
	// MaintenanceDB is the database the verifier issues CREATE/DROP DATABASE
	// against (it cannot do that from within the database being dumped).
	MaintenanceDB string
}

// DefaultDir is the on-host backup directory when ATLAS_BACKUP_DIR is unset.
const DefaultDir = "/var/lib/atlas/backups"

// ConfigFromEnv reads the backup config from the process environment.
func ConfigFromEnv() Config {
	return configFromLookup(os.LookupEnv)
}

// configFromLookup is the testable core of ConfigFromEnv.
func configFromLookup(lookup func(string) (string, bool)) Config {
	cfg := Config{
		TargetKind:     "local",
		Dir:            DefaultDir,
		Interval:       DefaultInterval,
		VerifyInterval: DefaultVerifyInterval,
		KeepDaily:      DefaultKeepDaily,
		KeepWeekly:     DefaultKeepWeekly,
		MaintenanceDB:  DefaultMaintenanceDB,
	}
	if v, ok := lookup(envTargetKind); ok && (v == "local" || v == "s3") {
		cfg.TargetKind = v
	}
	if v, ok := lookup(envBackupDir); ok && v != "" {
		cfg.Dir = v
	}
	if v, ok := lookup(envS3Bucket); ok {
		cfg.S3Bucket = v
	}
	if v, ok := lookup(envS3Prefix); ok {
		cfg.S3Prefix = v
	}
	if v, ok := lookup(envBackupInterval); ok {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.Interval = d
		}
	}
	if v, ok := lookup(envVerifyInterval); ok {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.VerifyInterval = d
		}
	}
	if v, ok := lookup(envKeepDaily); ok {
		if n, err := parsePositiveInt(v); err == nil {
			cfg.KeepDaily = n
		}
	}
	if v, ok := lookup(envKeepWeekly); ok {
		if n, err := parsePositiveInt(v); err == nil {
			cfg.KeepWeekly = n
		}
	}
	if v, ok := lookup(envVerifyMaintenance); ok && v != "" {
		cfg.MaintenanceDB = v
	}
	return cfg
}
