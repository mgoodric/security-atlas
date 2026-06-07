package backup

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// runner is the shared tick-loop driver mirroring the exception-expiry /
// metrics schedulers: fire immediately on start (so a fresh deploy gets first
// signal without waiting a full interval), then on every tick until ctx is
// cancelled. A sweep error is logged-and-dropped — one failed sweep never
// kills the loop (the failure is recorded in backup_runs + alerted).
func runner(ctx context.Context, interval time.Duration, logger *slog.Logger, name string, sweep func(context.Context) error) {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(discardWriter{}, nil))
	}
	if interval <= 0 {
		interval = DefaultInterval
	}
	logger.Info(name+" scheduler starting", "interval", interval.String())
	fire := func() {
		if err := sweep(ctx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error(name+" sweep", "err", err.Error())
		}
	}
	fire()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			logger.Info(name + " scheduler stopping")
			return
		case <-ticker.C:
			fire()
		}
	}
}

// RunBackup drives the backup tick-loop until ctx is cancelled.
func (b *Backuper) RunBackup(ctx context.Context, interval time.Duration) {
	runner(ctx, interval, b.logger, "backup", func(ctx context.Context) error {
		_, err := b.BackupOnce(ctx)
		return err
	})
}

// RunVerify drives the restore-verification tick-loop until ctx is cancelled.
func (v *Verifier) RunVerify(ctx context.Context, interval time.Duration) {
	runner(ctx, interval, v.logger, "restore-verify", func(ctx context.Context) error {
		_, err := v.VerifyOnce(ctx)
		return err
	})
}
