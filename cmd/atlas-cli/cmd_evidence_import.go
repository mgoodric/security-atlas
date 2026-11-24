package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/mgoodric/security-atlas/cmd/atlas-cli/cmdhttp"
	sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"
)

// maxImportLineBytes caps one JSON Lines record; ingest caps a payload at
// 1 MiB, so a line a little over that is the largest that can be accepted.
const maxImportLineBytes = 2 << 20

type evidenceImportFlags struct {
	historical  bool
	source      string
	amendFrozen []string
	amendReason string
	jsonOut     bool
}

func newEvidenceImportCmd() *cobra.Command {
	var f evidenceImportFlags
	cmd := &cobra.Command{
		Use:   "import --historical <file.jsonl>",
		Short: "import historical evidence with its real observed_at (admin only)",
		Long: `Import prior-period evidence (last year's audit results, history from a
spreadsheet or another GRC tool) with its real observed_at. Admin credential
only. The file is JSON Lines: one record per line, in the same envelope
POST /v1/evidence:push accepts. --source names where the batch came from.

The batch is all or nothing. Records keep their past observed_at; one more
than 24h in the future is still refused. A record dated inside an open audit
period joins it. A record dated inside a frozen audit period fails the whole
import, unless that period is named with --amend-frozen: then the period is
re-frozen as a new freeze revision that adds the batch's records, and the
original freeze stays recorded. --amend-reason is then required: it is shown
with the amended-by-import notice on the period and its samples.`,
		Args: cobra.ExactArgs(1),
		PreRunE: func(_ *cobra.Command, _ []string) error {
			if !f.historical {
				return fmt.Errorf("--historical is required: it is the only import this command runs")
			}
			if strings.TrimSpace(f.source) == "" {
				return fmt.Errorf("--source is required (e.g. \"FY25 SOC 2 Type II report\")")
			}
			if len(f.amendFrozen) > 0 && strings.TrimSpace(f.amendReason) == "" {
				return fmt.Errorf("--amend-frozen needs --amend-reason: why the frozen period is being amended")
			}
			for _, id := range f.amendFrozen {
				if _, err := uuid.Parse(id); err != nil {
					return fmt.Errorf("--amend-frozen %q is not a UUID", id)
				}
			}
			return resolveCommon()
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			records, err := readImportLines(args[0])
			if err != nil {
				return err
			}
			raw, err := postEvidenceImport(f, records)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if f.jsonOut {
				var pretty bytes.Buffer
				if err := json.Indent(&pretty, raw, "", "  "); err != nil {
					return fmt.Errorf("format response: %w", err)
				}
				_, _ = fmt.Fprintln(out, pretty.String())
				return nil
			}
			return renderEvidenceImport(out, raw)
		},
	}
	cmd.Flags().BoolVar(&f.historical, "historical", false, "import past-dated evidence (required)")
	cmd.Flags().StringVar(&f.source, "source", "", "where the batch came from, e.g. \"FY25 SOC 2 Type II report\" [required]")
	cmd.Flags().StringArrayVar(&f.amendFrozen, "amend-frozen", nil, "frozen audit period ID the import may re-freeze as a new revision; repeatable")
	cmd.Flags().StringVar(&f.amendReason, "amend-reason", "", "why the frozen periods are being amended; required with --amend-frozen, stored on each revision")
	cmd.Flags().BoolVar(&f.jsonOut, "json", false, "print the API response as JSON")
	return cmd
}

// readImportLines reads a JSON Lines file into one raw JSON object per
// non-blank line. It checks only that each line is a JSON object; the server
// validates the envelope.
func readImportLines(path string) ([]json.RawMessage, error) {
	fh, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = fh.Close() }()
	return parseImportLines(fh)
}

func parseImportLines(r io.Reader) ([]json.RawMessage, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), maxImportLineBytes)
	var out []json.RawMessage
	line := 0
	for sc.Scan() {
		line++
		b := bytes.TrimSpace(sc.Bytes())
		if len(b) == 0 {
			continue
		}
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(b, &obj); err != nil {
			return nil, fmt.Errorf("line %d: not a JSON object: %w", line, err)
		}
		out = append(out, json.RawMessage(append([]byte(nil), b...)))
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("line %d: %w", line+1, err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no records in file")
	}
	return out, nil
}

func postEvidenceImport(f evidenceImportFlags, records []json.RawMessage) ([]byte, error) {
	body, err := json.Marshal(map[string]any{
		"source":       f.source,
		"amend_frozen": f.amendFrozen,
		"amend_reason": f.amendReason,
		"records":      records,
	})
	if err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}
	url := strings.TrimRight(common.endpoint, "/") + "/v1/evidence:import"
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(sdk.MetadataAuthorization, sdk.BearerPrefix+common.token)

	// A large batch commits in one transaction; allow it time. Guard refuses
	// plain http:// to a non-loopback endpoint (#2257).
	client := &http.Client{Timeout: 10 * time.Minute, Transport: cmdhttp.Guard(nil)}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("import: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, importError(resp.StatusCode, raw)
	}
	return raw, nil
}

// importError renders a refused import, listing the frozen periods a
// frozen_audit_period refusal names.
func importError(status int, raw []byte) error {
	var e struct {
		Error         string `json:"error"`
		Code          string `json:"code"`
		FrozenPeriods []struct {
			AuditPeriodID string `json:"audit_period_id"`
			Name          string `json:"name"`
			Records       int    `json:"records"`
		} `json:"frozen_periods"`
	}
	if json.Unmarshal(raw, &e) != nil || e.Error == "" {
		return fmt.Errorf("import failed: HTTP %d: %s", status, strings.TrimSpace(string(raw)))
	}
	if e.Code != "frozen_audit_period" {
		return fmt.Errorf("import failed: HTTP %d: %s", status, e.Error)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "import failed: nothing was written. Records fall in frozen audit periods:\n")
	for _, p := range e.FrozenPeriods {
		fmt.Fprintf(&b, "  %s  %s  (%d records)\n", p.AuditPeriodID, printable(p.Name), p.Records)
	}
	b.WriteString("Re-run with --amend-frozen <id> for each period and --amend-reason \"<why>\" to re-freeze them as a new revision.")
	return fmt.Errorf("%s", b.String())
}

func renderEvidenceImport(out io.Writer, raw []byte) error {
	var res struct {
		ImportBatchID string `json:"import_batch_id"`
		Receipts      []struct {
			Deduplicated bool `json:"deduplicated"`
		} `json:"receipts"`
		Amendments []struct {
			AuditPeriodID  string `json:"audit_period_id"`
			Revision       int    `json:"revision"`
			FrozenHash     string `json:"frozen_hash"`
			PreviousHash   string `json:"previous_hash"`
			EvidenceAddedN int    `json:"evidence_added_n"`
		} `json:"amendments"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	dedup := 0
	for _, r := range res.Receipts {
		if r.Deduplicated {
			dedup++
		}
	}
	_, _ = fmt.Fprintf(out, "import batch %s: %d records (%d new, %d already in the ledger)\n",
		res.ImportBatchID, len(res.Receipts), len(res.Receipts)-dedup, dedup)
	for _, a := range res.Amendments {
		_, _ = fmt.Fprintf(out, "re-froze audit period %s as revision %d: +%d records, hash %s (was %s)\n",
			a.AuditPeriodID, a.Revision, a.EvidenceAddedN, a.FrozenHash, a.PreviousHash)
	}
	return nil
}
