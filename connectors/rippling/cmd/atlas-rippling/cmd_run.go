package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/spf13/cobra"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"

	"github.com/mgoodric/security-atlas/connectors/hris/hierarchy"
	"github.com/mgoodric/security-atlas/connectors/hris/hierarchyrecord"
	"github.com/mgoodric/security-atlas/connectors/hris/worker"
	"github.com/mgoodric/security-atlas/connectors/hris/workerrecord"
	"github.com/mgoodric/security-atlas/connectors/rippling/internal/ripplingauth"
	"github.com/mgoodric/security-atlas/connectors/rippling/internal/workers"
)

// Package-level seams: doRun reaches through these function variables so tests
// can swap in fakes for the Rippling read + the sdk client constructor without
// hitting live Rippling or a real platform endpoint. Production code paths are
// byte-for-byte unchanged; only the call-site indirection moved.
var (
	workersCollect = workers.Collect
	newSDKClient   = func(endpoint, bearer string, opts ...sdk.Option) (sdkPushClient, error) {
		return sdk.NewClient(endpoint, bearer, opts...)
	}
	// newWorkersAPI builds the live read-only HTTP client; seamed so tests inject
	// a fake.
	newWorkersAPI = func(hc *http.Client, baseURL, apiToken string) workers.API {
		return workers.NewClient(hc, baseURL, apiToken)
	}
)

// sdkPushClient is the narrow surface doRun consumes from sdk.Client.
type sdkPushClient interface {
	Push(ctx context.Context, record *evidencev1.EvidenceRecord) (*evidencev1.EvidenceReceipt, error)
	Close() error
}

type runFlags struct {
	environment      string
	workerControl    string
	hierarchyControl string
	baseURL          string
}

func newRunCmd() *cobra.Command {
	var f runFlags
	cmd := &cobra.Command{
		Use:   "run",
		Short: "read Rippling worker-lifecycle records and push evidence records",
		Long: `Read Rippling worker-lifecycle records via the read-only Rippling
employee-directory API (GET /platform/api/employees, requesting only the minimal
worker-lifecycle fields), transform to hris.worker_lifecycle.v1 records, and push
to the platform.

Profile: pull. One bounded read-and-push pass per invocation; operator-scheduled
(recommended 24h). NOT continuous monitoring.

Auth: set RIPPLING_API_TOKEN (the token must be scoped read-only to the
worker-lifecycle field group). The token never appears in a log line or an
evidence record. The connector emits worker-lifecycle facts only — never SSN,
compensation, home address, bank/payment, benefits, performance, date of birth,
or personal contact detail.`,
		SilenceErrors: true,
		SilenceUsage:  true,
		PreRunE: func(_ *cobra.Command, _ []string) error {
			if f.environment == "" {
				return errors.New("--environment is required (records must be scoped)")
			}
			return resolveCommon()
		},
		RunE: func(_ *cobra.Command, _ []string) error {
			return doRun(context.Background(), f)
		},
	}
	cmd.Flags().StringVar(&f.environment, "environment", "", "environment scope tag [required]")
	cmd.Flags().StringVar(&f.workerControl, "worker-control", "scf:IAC-22", "control_id to attach to hris.worker_lifecycle.v1 records")
	cmd.Flags().StringVar(&f.hierarchyControl, "hierarchy-control", "scf:IAC-22", "control_id to attach to hris.manager_hierarchy.v1 records")
	cmd.Flags().StringVar(&f.baseURL, "base-url", "", "Rippling API base URL override (env: RIPPLING_BASE_URL)")
	return cmd
}

func doRun(ctx context.Context, f runFlags) error {
	cred, err := ripplingauth.Resolve(ripplingauth.ResolveOpts{BaseURL: f.baseURL})
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}

	httpClient := &http.Client{Timeout: 30 * time.Second}
	sdkClient, err := newSDKClient(common.endpoint, common.token, sdkOpts()...)
	if err != nil {
		return fmt.Errorf("sdk client: %w", err)
	}
	defer func() { _ = sdkClient.Close() }()

	api := newWorkersAPI(httpClient, cred.BaseURL(), cred.APIToken())
	raw, err := workersCollect(ctx, api)
	if err != nil {
		return fmt.Errorf("rippling collect: %w", err)
	}
	wks := worker.Normalize(worker.HRISRippling, raw, nil)

	pushed := 0
	for _, w := range wks {
		rec, err := workerrecord.Build(w, f.workerControl, actorID("workers"), "rippling", f.environment)
		if err != nil {
			return fmt.Errorf("build record %s: %w", w.WorkerID, err)
		}
		if err := pushOne(ctx, sdkClient, rec); err != nil {
			return fmt.Errorf("push worker %s: %w", w.WorkerID, err)
		}
		pushed++
	}

	// Manager-hierarchy evidence (slice 571): derive the reporting tree from the
	// SAME bounded roster (no new source read) and push one edge record per
	// worker, sharing the roster's observed_at so a tree's records carry one
	// consistent timestamp. rosterObservedAt is the hour-truncated observation
	// time worker.Normalize stamped on every worker.
	hpushed := 0
	if len(wks) > 0 {
		rosterObservedAt := wks[0].ObservedAt
		for _, e := range hierarchy.Build(wks) {
			rec, err := hierarchyrecord.Build(e, worker.HRISRippling, f.hierarchyControl, actorID("hierarchy"), "rippling", f.environment, rosterObservedAt)
			if err != nil {
				return fmt.Errorf("build hierarchy record %s: %w", e.WorkerAssignmentID, err)
			}
			if err := pushOne(ctx, sdkClient, rec); err != nil {
				return fmt.Errorf("push hierarchy %s: %w", e.WorkerAssignmentID, err)
			}
			hpushed++
		}
	}

	fmt.Printf("pushed %d worker-lifecycle + %d manager-hierarchy records (hris=rippling environment=%s)\n", pushed, hpushed, f.environment)
	return nil
}

func pushOne(ctx context.Context, client sdkPushClient, rec *evidencev1.EvidenceRecord) error {
	pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err := client.Push(pctx, rec)
	return err
}
