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

	"github.com/mgoodric/security-atlas/connectors/bamboohr/internal/bamboohrauth"
	"github.com/mgoodric/security-atlas/connectors/bamboohr/internal/workers"
	"github.com/mgoodric/security-atlas/connectors/hris/worker"
	"github.com/mgoodric/security-atlas/connectors/hris/workerrecord"
)

// Package-level seams: doRun reaches through these function variables so tests
// can swap in fakes for the BambooHR read + the sdk client constructor without
// hitting live BambooHR or a real platform endpoint. Production code paths are
// byte-for-byte unchanged; only the call-site indirection moved.
var (
	workersCollect = workers.Collect
	newSDKClient   = func(endpoint, bearer string, opts ...sdk.Option) (sdkPushClient, error) {
		return sdk.NewClient(endpoint, bearer, opts...)
	}
	// newWorkersAPI builds the live read-only HTTP client; seamed so tests inject
	// a fake.
	newWorkersAPI = func(hc *http.Client, baseURL, companyDomain, apiKey string) workers.API {
		return workers.NewClient(hc, baseURL, companyDomain, apiKey)
	}
)

// sdkPushClient is the narrow surface doRun consumes from sdk.Client.
type sdkPushClient interface {
	Push(ctx context.Context, record *evidencev1.EvidenceRecord) (*evidencev1.EvidenceReceipt, error)
	Close() error
}

type runFlags struct {
	environment   string
	workerControl string
	baseURL       string
}

func newRunCmd() *cobra.Command {
	var f runFlags
	cmd := &cobra.Command{
		Use:   "run",
		Short: "read BambooHR worker-lifecycle records and push evidence records",
		Long: `Read BambooHR worker-lifecycle records via the read-only BambooHR
custom-report API (GET /v1/reports/custom, requesting only the minimal
worker-lifecycle fields), transform to hris.worker_lifecycle.v1 records, and push
to the platform.

Profile: pull. One bounded read-and-push pass per invocation; operator-scheduled
(recommended 24h). NOT continuous monitoring.

Auth: set BAMBOOHR_API_KEY + BAMBOOHR_COMPANY_DOMAIN (the key must be for a
read-only worker-directory role). The key never appears in a log line or an
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
	cmd.Flags().StringVar(&f.baseURL, "base-url", "", "BambooHR API base URL override (env: BAMBOOHR_BASE_URL)")
	return cmd
}

func doRun(ctx context.Context, f runFlags) error {
	cred, err := bamboohrauth.Resolve(bamboohrauth.ResolveOpts{BaseURL: f.baseURL})
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}

	httpClient := &http.Client{Timeout: 30 * time.Second}
	sdkClient, err := newSDKClient(common.endpoint, common.token, sdkOpts()...)
	if err != nil {
		return fmt.Errorf("sdk client: %w", err)
	}
	defer func() { _ = sdkClient.Close() }()

	api := newWorkersAPI(httpClient, cred.BaseURL(), cred.CompanyDomain(), cred.APIKey())
	raw, err := workersCollect(ctx, api)
	if err != nil {
		return fmt.Errorf("bamboohr collect: %w", err)
	}
	wks := worker.Normalize(worker.HRISBambooHR, raw, nil)

	pushed := 0
	for _, w := range wks {
		rec, err := workerrecord.Build(w, f.workerControl, actorID("workers"), "bamboohr", f.environment)
		if err != nil {
			return fmt.Errorf("build record %s: %w", w.WorkerID, err)
		}
		if err := pushOne(ctx, sdkClient, rec); err != nil {
			return fmt.Errorf("push worker %s: %w", w.WorkerID, err)
		}
		pushed++
	}

	fmt.Printf("pushed %d records (hris=bamboohr environment=%s)\n", pushed, f.environment)
	return nil
}

func pushOne(ctx context.Context, client sdkPushClient, rec *evidencev1.EvidenceRecord) error {
	pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err := client.Push(pctx, rec)
	return err
}
