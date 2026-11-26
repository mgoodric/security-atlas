package main

// #1797: `atlas-cli mappings overlay` manages the organisation's overlay
// edges (its own requirement -> SCF anchor edges, #1735) through the HTTP
// API (/v1/mapping-overlays). Every subcommand takes --endpoint and --token
// like the taxonomy commands; the tenant is the token's, never a flag.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/mgoodric/security-atlas/cmd/atlas-cli/cmdhttp"
	sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"
)

func newMappingsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mappings",
		Short: "manage the organisation's framework mappings",
	}
	cmd.AddCommand(newMappingsOverlayCmd())
	return cmd
}

func newMappingsOverlayCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "overlay",
		Short: "add, list, retire, import or export the organisation's overlay edges (requirement -> SCF anchor)",
		Long: "An overlay edge is the organisation's own link from a framework requirement to an SCF anchor, " +
			"such as a criterion its auditor tested a control under. Overlay edges belong to the token's " +
			"tenant only. Adding, retiring and importing need an admin or grc_engineer token and are audit-logged.",
	}
	cmd.AddCommand(newMappingsOverlayAddCmd(), newMappingsOverlayListCmd(), newMappingsOverlayRetireCmd(),
		newMappingsOverlayImportCmd(), newMappingsOverlayExportCmd(), newMappingsOverlayAuditedUnderCmd())
	return cmd
}

// auditedUnderReport is GET /v1/mapping-overlays/audited-under's body, the
// fields the table prints (#2324).
type auditedUnderReport struct {
	Rows []struct {
		ControlBundleID  string `json:"control_bundle_id"`
		AnchorCode       string `json:"anchor_code"`
		AnchorSCFRelease string `json:"anchor_scf_release"`
		AnchorFit        string `json:"anchor_fit"`
		Framework        string `json:"framework"`
		FrameworkVersion string `json:"framework_version"`
		RequirementCode  string `json:"requirement_code"`
		TestedUnderNote  string `json:"tested_under_note"`
		CandidateAnchors []struct {
			AnchorCode string `json:"anchor_code"`
		} `json:"candidate_anchors"`
	} `json:"rows"`
	Count                int  `json:"count"`
	Total                int  `json:"total"`
	Truncated            bool `json:"truncated"`
	UnresolvedReferences []struct {
		ControlBundleID string `json:"control_bundle_id"`
		System          string `json:"system"`
		Code            string `json:"code"`
		TestedUnderNote string `json:"tested_under_note"`
	} `json:"unresolved_references"`
	UnresolvedCount int `json:"unresolved_count"`
}

func newMappingsOverlayAuditedUnderCmd() *cobra.Command {
	var framework, outPath string
	var limit int
	var jsonOutput, csvOutput bool
	cmd := &cobra.Command{
		Use:   "audited-under",
		Short: "list criteria your controls were tested under that their SCF anchor does not reach",
		Long: "The audited-under report: each row is a control and a criterion it was tested under " +
			"(its tested_under reference) that its SCF anchor does not reach through catalog and overlay " +
			"edges. Add an overlay edge to close a row. References that name no requirement in the " +
			"framework's current version are listed after the table.",
		Example: "  atlas-cli mappings overlay audited-under --framework soc2\n" +
			"  atlas-cli mappings overlay audited-under --csv -o audited-under.csv",
		Args:    cobra.NoArgs,
		PreRunE: func(_ *cobra.Command, _ []string) error { return resolveCommon() },
		RunE: func(cmd *cobra.Command, _ []string) error {
			q := url.Values{}
			if framework != "" {
				q.Set("framework", framework)
			}
			if limit != 0 {
				q.Set("limit", strconv.Itoa(limit))
			}
			path := "/v1/mapping-overlays/audited-under"
			if csvOutput {
				path += "/export"
			}
			if len(q) > 0 {
				path += "?" + q.Encode()
			}
			out := cmd.OutOrStdout()
			if csvOutput {
				code, hdr, raw, err := overlayRawRequestWithHeaders(http.MethodGet, path, nil)
				if err != nil {
					return err
				}
				if code != http.StatusOK {
					return apiError(code, raw)
				}
				// The CSV has no room to say it was cut, so say it on stderr.
				if hdr.Get("X-Atlas-Truncated") == "true" {
					_, _ = fmt.Fprintln(cmd.ErrOrStderr(),
						"warning: the report was truncated at its row cap; narrow it with --framework")
				}
				if n := hdr.Get("X-Atlas-Unresolved-References"); n != "" && n != "0" {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
						"warning: %s tested-under reference(s) do not resolve; run without --csv to list them\n", printable(n))
				}
				if outPath == "" {
					_, err = out.Write(raw)
					return err
				}
				if err := os.WriteFile(outPath, raw, 0o600); err != nil {
					return fmt.Errorf("write %s: %w", outPath, err)
				}
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "wrote %s\n", outPath)
				return nil
			}
			raw, err := taxonomyRequest(http.MethodGet, path, nil)
			if err != nil {
				return err
			}
			if jsonOutput {
				return printIndented(out, raw)
			}
			var rep auditedUnderReport
			if err := json.Unmarshal(raw, &rep); err != nil {
				return fmt.Errorf("decode response: %w", err)
			}
			return printAuditedUnder(out, rep)
		},
	}
	cmd.Flags().StringVar(&framework, "framework", "", "only this framework slug")
	cmd.Flags().IntVar(&limit, "limit", 0, "at most this many rows, 1 to 5000 (default: the platform's cap, 5000)")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "print the API response as JSON")
	cmd.Flags().BoolVar(&csvOutput, "csv", false, "print the report as CSV (the export endpoint)")
	cmd.Flags().StringVarP(&outPath, "out", "o", "", "with --csv, write the CSV to this file (default: stdout)")
	cmd.MarkFlagsMutuallyExclusive("json", "csv")
	return cmd
}

// printAuditedUnder prints the report's rows, then any unresolved
// references, then a line when the report was cut at its cap.
func printAuditedUnder(out io.Writer, rep auditedUnderReport) error {
	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "CRITERION\tCONTROL\tANCHOR\tFIT\tCANDIDATES\tTESTED UNDER")
	for _, r := range rep.Rows {
		anchor := "-"
		if r.AnchorCode != "" {
			anchor = "SCF " + r.AnchorSCFRelease + ":" + r.AnchorCode
		}
		cands := make([]string, len(r.CandidateAnchors))
		for i, c := range r.CandidateAnchors {
			cands[i] = c.AnchorCode
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			printable(r.Framework+":"+r.FrameworkVersion+":"+r.RequirementCode), printable(r.ControlBundleID),
			printable(anchor), printable(r.AnchorFit), printable(strings.Join(cands, ", ")), printable(r.TestedUnderNote))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if rep.UnresolvedCount > 0 {
		_, _ = fmt.Fprintf(out, "\n%d tested-under reference(s) name no requirement in the framework's current version:\n", rep.UnresolvedCount)
		for _, u := range rep.UnresolvedReferences {
			line := "  " + printable(u.System+" "+u.Code) + " on " + printable(u.ControlBundleID)
			if u.TestedUnderNote != "" {
				line += " (" + printable(u.TestedUnderNote) + ")"
			}
			_, _ = fmt.Fprintln(out, line)
		}
	}
	if rep.Truncated {
		_, _ = fmt.Fprintf(out, "\ntruncated: showing %d of %d row(s) and %d of %d unresolved reference(s)\n",
			rep.Count, rep.Total, len(rep.UnresolvedReferences), rep.UnresolvedCount)
	}
	return nil
}

// overlayEdge is an overlay edge as the API returns it.
type overlayEdge struct {
	ID               string  `json:"id"`
	Framework        string  `json:"framework"`
	FrameworkVersion string  `json:"framework_version"`
	RequirementCode  string  `json:"requirement_code"`
	SCFRelease       string  `json:"scf_release"`
	AnchorCode       string  `json:"anchor_code"`
	RelationshipType string  `json:"relationship_type"`
	Strength         float64 `json:"strength"`
	Rationale        string  `json:"rationale"`
	SourceRef        string  `json:"source_ref"`
	Status           string  `json:"status"`
	RetirementReason string  `json:"retirement_reason"`
}

func newMappingsOverlayAddCmd() *cobra.Command {
	var (
		frameworkVersion, scfRelease, relationship, rationale, source string
		strength                                                      float64
	)
	cmd := &cobra.Command{
		Use:   "add <framework> <requirement-code> <anchor-code> --relationship <r> --strength <s> --rationale <text> --source <ref>",
		Short: "add an overlay edge from a framework requirement to an SCF anchor",
		Example: "  atlas-cli mappings overlay add soc2 CC1.1 HRS-07.1 --framework-version 2017 --scf-release 2025.2 \\\n" +
			"    --relationship intersects_with --strength 0.8 \\\n" +
			"    --rationale 'Auditor tested the confidentiality agreement control under CC1.1' \\\n" +
			"    --source 'FY25 SOC 2 report, section 4, CC1.1'",
		Args:    cobra.ExactArgs(3),
		PreRunE: func(_ *cobra.Command, _ []string) error { return resolveCommon() },
		RunE: func(cmd *cobra.Command, args []string) error {
			if rationale == "" {
				return fmt.Errorf("--rationale is required")
			}
			if source == "" {
				return fmt.Errorf("--source is required")
			}
			raw, err := taxonomyRequest(http.MethodPost, "/v1/mapping-overlays", map[string]any{
				"framework":         args[0],
				"framework_version": frameworkVersion,
				"requirement_code":  args[1],
				"scf_release":       scfRelease,
				"anchor_code":       args[2],
				"relationship_type": relationship,
				"strength":          strength,
				"rationale":         rationale,
				"source_ref":        source,
			})
			if err != nil {
				return err
			}
			return printOverlayResult(cmd.OutOrStdout(), "added", raw)
		},
	}
	cmd.Flags().StringVar(&frameworkVersion, "framework-version", "", "framework version label (default: the current version)")
	cmd.Flags().StringVar(&scfRelease, "scf-release", "", "SCF release the anchor code is in, e.g. test-2026.3 (required)")
	cmd.Flags().StringVar(&relationship, "relationship", "", "STRM relationship: equal, subset_of, superset_of or intersects_with (required)")
	cmd.Flags().Float64Var(&strength, "strength", 0, "strength, 0 to 1 (required)")
	cmd.Flags().StringVar(&rationale, "rationale", "", "why the requirement maps to the anchor (required)")
	cmd.Flags().StringVar(&source, "source", "", "the evidence for the mapping, e.g. an audit report section (required)")
	_ = cmd.MarkFlagRequired("scf-release")
	_ = cmd.MarkFlagRequired("relationship")
	_ = cmd.MarkFlagRequired("strength")
	return cmd
}

func newMappingsOverlayListCmd() *cobra.Command {
	var framework, requirement, anchor, status string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:     "list",
		Short:   "list the organisation's overlay edges",
		Args:    cobra.NoArgs,
		PreRunE: func(_ *cobra.Command, _ []string) error { return resolveCommon() },
		RunE: func(cmd *cobra.Command, _ []string) error {
			q := url.Values{}
			for k, v := range map[string]string{
				"framework": framework, "requirement": requirement, "anchor": anchor, "status": status,
			} {
				if v != "" {
					q.Set(k, v)
				}
			}
			path := "/v1/mapping-overlays"
			if len(q) > 0 {
				path += "?" + q.Encode()
			}
			raw, err := taxonomyRequest(http.MethodGet, path, nil)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if jsonOutput {
				return printIndented(out, raw)
			}
			var resp struct {
				OverlayEdges []overlayEdge `json:"overlay_edges"`
			}
			if err := json.Unmarshal(raw, &resp); err != nil {
				return fmt.Errorf("decode response: %w", err)
			}
			tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
			_, _ = fmt.Fprintln(tw, "ID\tREQUIREMENT\tANCHOR\tRELATIONSHIP\tSTRENGTH\tSTATUS\tSOURCE")
			for _, e := range resp.OverlayEdges {
				_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
					printable(e.ID), overlayRequirement(e), overlayAnchor(e), printable(e.RelationshipType),
					strconv.FormatFloat(e.Strength, 'f', -1, 64), printable(e.Status), printable(e.SourceRef))
			}
			return tw.Flush()
		},
	}
	cmd.Flags().StringVar(&framework, "framework", "", "only this framework slug")
	cmd.Flags().StringVar(&requirement, "requirement", "", "only this requirement code")
	cmd.Flags().StringVar(&anchor, "anchor", "", "only this SCF anchor code")
	cmd.Flags().StringVar(&status, "status", "", "only active or retired edges")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "print the API response as JSON")
	return cmd
}

func newMappingsOverlayRetireCmd() *cobra.Command {
	var reason string
	cmd := &cobra.Command{
		Use:     "retire <id> --reason <text>",
		Short:   "retire an overlay edge; it stops counting and keeps its history",
		Args:    cobra.ExactArgs(1),
		PreRunE: func(_ *cobra.Command, _ []string) error { return resolveCommon() },
		RunE: func(cmd *cobra.Command, args []string) error {
			if reason == "" {
				return fmt.Errorf("--reason is required")
			}
			raw, err := taxonomyRequest(http.MethodPost,
				"/v1/mapping-overlays/"+url.PathEscape(args[0])+"/retire", map[string]string{"reason": reason})
			if err != nil {
				return err
			}
			return printOverlayResult(cmd.OutOrStdout(), "retired", raw)
		},
	}
	cmd.Flags().StringVar(&reason, "reason", "", "why the edge is retired (required)")
	return cmd
}

func overlayRequirement(e overlayEdge) string {
	return printable(e.Framework + ":" + e.FrameworkVersion + ":" + e.RequirementCode)
}

func overlayAnchor(e overlayEdge) string {
	return printable("SCF " + e.SCFRelease + ":" + e.AnchorCode)
}

func printOverlayResult(out io.Writer, verb string, raw []byte) error {
	var resp struct {
		OverlayEdge overlayEdge `json:"overlay_edge"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	e := resp.OverlayEdge
	_, _ = fmt.Fprintf(out, "%s %s: %s -> %s [%s]\n", verb, printable(e.ID), overlayRequirement(e), overlayAnchor(e), printable(e.Status))
	return nil
}

// maxOverlayFileBytes mirrors the API's import body cap, so an oversized
// file is refused before it is sent.
const maxOverlayFileBytes = 4 << 20

func newMappingsOverlayImportCmd() *cobra.Command {
	var format string
	var partial bool
	cmd := &cobra.Command{
		Use:   "import <file>",
		Short: "import overlay edges from a CSV or YAML file",
		Long: "Each row is an overlay edge with the columns framework, framework_version, requirement_code, " +
			"scf_release, anchor_code, relationship_type, strength, rationale and source_ref. Every row is " +
			"checked as a single add is. By default nothing is written if any row is bad; with --partial the " +
			"valid rows are written. Bad rows are printed with their row number and reason.",
		Example: "  atlas-cli mappings overlay import overlays.csv\n" +
			"  atlas-cli mappings overlay import overlays.yaml --partial",
		Args:    cobra.ExactArgs(1),
		PreRunE: func(_ *cobra.Command, _ []string) error { return resolveCommon() },
		RunE: func(cmd *cobra.Command, args []string) error {
			if format == "" {
				switch strings.ToLower(filepath.Ext(args[0])) {
				case ".csv":
					format = "csv"
				case ".yaml", ".yml":
					format = "yaml"
				default:
					return fmt.Errorf("cannot tell the format from %q: pass --format csv or --format yaml", filepath.Base(args[0]))
				}
			}
			if format != "csv" && format != "yaml" {
				return fmt.Errorf("--format must be csv or yaml")
			}
			body, err := readOverlayFile(args[0])
			if err != nil {
				return err
			}
			q := url.Values{"format": {format}}
			if partial {
				q.Set("partial", "true")
			}
			status, raw, err := overlayRawRequest(http.MethodPost, "/v1/mapping-overlays/import?"+q.Encode(), body)
			if err != nil {
				return err
			}
			return printOverlayImport(cmd.OutOrStdout(), status, raw)
		},
	}
	cmd.Flags().StringVar(&format, "format", "", "csv or yaml (default: from the file extension)")
	cmd.Flags().BoolVar(&partial, "partial", false, "write the valid rows even if some rows are bad")
	return cmd
}

func newMappingsOverlayExportCmd() *cobra.Command {
	var framework, requirement, anchor, status, outPath string
	cmd := &cobra.Command{
		Use:     "export",
		Short:   "export the organisation's overlay edges as CSV",
		Args:    cobra.NoArgs,
		PreRunE: func(_ *cobra.Command, _ []string) error { return resolveCommon() },
		RunE: func(cmd *cobra.Command, _ []string) error {
			q := url.Values{}
			for k, v := range map[string]string{
				"framework": framework, "requirement": requirement, "anchor": anchor, "status": status,
			} {
				if v != "" {
					q.Set(k, v)
				}
			}
			path := "/v1/mapping-overlays/export"
			if len(q) > 0 {
				path += "?" + q.Encode()
			}
			code, raw, err := overlayRawRequest(http.MethodGet, path, nil)
			if err != nil {
				return err
			}
			if code != http.StatusOK {
				return apiError(code, raw)
			}
			if outPath == "" {
				_, err = cmd.OutOrStdout().Write(raw)
				return err
			}
			if err := os.WriteFile(outPath, raw, 0o600); err != nil {
				return fmt.Errorf("write %s: %w", outPath, err)
			}
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "wrote %s\n", outPath)
			return nil
		},
	}
	cmd.Flags().StringVar(&framework, "framework", "", "only this framework slug")
	cmd.Flags().StringVar(&requirement, "requirement", "", "only this requirement code")
	cmd.Flags().StringVar(&anchor, "anchor", "", "only this SCF anchor code")
	cmd.Flags().StringVar(&status, "status", "", "only active or retired edges")
	cmd.Flags().StringVarP(&outPath, "out", "o", "", "write the CSV to this file (default: stdout)")
	return cmd
}

func readOverlayFile(path string) ([]byte, error) {
	f, err := os.Open(path) // #nosec G304 -- the operator names the file to import.
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	body, err := io.ReadAll(io.LimitReader(f, maxOverlayFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(body) > maxOverlayFileBytes {
		return nil, fmt.Errorf("%s exceeds the 4 MiB import limit", path)
	}
	return body, nil
}

// overlayRawRequest is taxonomyRequest for a non-JSON body or response: it
// sends body as-is and hands back the status, so the caller can read a
// 422's row report.
func overlayRawRequest(method, path string, body []byte) (int, []byte, error) {
	code, _, raw, err := overlayRawRequestWithHeaders(method, path, body)
	return code, raw, err
}

// overlayRawRequestWithHeaders is overlayRawRequest that also hands back
// the response headers, for a caller that reads a signal from them.
func overlayRawRequestWithHeaders(method, path string, body []byte) (int, http.Header, []byte, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, strings.TrimRight(common.endpoint, "/")+path, reader)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set(sdk.MetadataAuthorization, sdk.BearerPrefix+common.token)
	resp, err := cmdhttp.Client(60 * time.Second).Do(req)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024*1024))
	if err != nil {
		return 0, nil, nil, fmt.Errorf("read response: %w", err)
	}
	return resp.StatusCode, resp.Header, raw, nil
}

// apiError is the error taxonomyRequest returns for a 4xx/5xx.
func apiError(status int, raw []byte) error {
	var e struct {
		Error string `json:"error"`
	}
	msg := strings.TrimSpace(string(raw))
	if json.Unmarshal(raw, &e) == nil && e.Error != "" {
		msg = e.Error
	}
	return fmt.Errorf("HTTP %d: %s", status, printable(msg))
}

// printOverlayImport prints what an import wrote and every rejected row.
// It is an error when nothing was written.
func printOverlayImport(out io.Writer, status int, raw []byte) error {
	if status != http.StatusCreated && status != http.StatusUnprocessableEntity {
		return apiError(status, raw)
	}
	var resp struct {
		Written bool `json:"written"`
		Created []struct {
			Row         int         `json:"row"`
			OverlayEdge overlayEdge `json:"overlay_edge"`
		} `json:"created"`
		Rejected []struct {
			Row    int    `json:"row"`
			Line   int    `json:"line"`
			Code   string `json:"code"`
			Reason string `json:"reason"`
		} `json:"rejected"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	for _, c := range resp.Created {
		_, _ = fmt.Fprintf(out, "row %d: added %s: %s -> %s\n", c.Row, printable(c.OverlayEdge.ID),
			overlayRequirement(c.OverlayEdge), overlayAnchor(c.OverlayEdge))
	}
	for _, rj := range resp.Rejected {
		_, _ = fmt.Fprintf(out, "row %d (line %d): rejected: %s: %s\n", rj.Row, rj.Line, printable(rj.Code), printable(rj.Reason))
	}
	if !resp.Written {
		return fmt.Errorf("nothing imported: %d row(s) rejected", len(resp.Rejected))
	}
	_, _ = fmt.Fprintf(out, "imported %d overlay edge(s), %d row(s) rejected\n", len(resp.Created), len(resp.Rejected))
	return nil
}
