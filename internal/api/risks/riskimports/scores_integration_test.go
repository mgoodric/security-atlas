//go:build integration

// #2841 — the batch's one scoring methodology and the scores read from the
// register's columns, through the API against real Postgres: each
// methodology stages scored, unscored and invalid rows, the batch shows its
// methodology, and approval creates each risk with the batch methodology and
// the staged scores.
package riskimports_test

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/risk/riskimport"
)

// uploadWithMethodology posts a register with a methodology field (omitted
// when empty).
func uploadWithMethodology(t *testing.T, e env, bearer, methodology string, payload []byte) (int, []byte) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file", "register.csv")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write(payload); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if methodology != "" {
		_ = mw.WriteField("methodology", methodology)
	}
	_ = mw.Close()
	req, err := http.NewRequest(http.MethodPost, e.server.URL+"/v1/risks/imports", &body)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+bearer)
	return do(t, req)
}

// storedRisk is a created risk's scoring as the risks table holds it.
type storedRisk struct {
	Methodology        string
	Inherent, Residual map[string]any
}

func readStoredRisk(t *testing.T, e env, id string) storedRisk {
	t.Helper()
	var s storedRisk
	var inh, res []byte
	if err := e.admin.QueryRow(context.Background(),
		`SELECT methodology::text, inherent_score, residual_score FROM risks WHERE id = $1::uuid AND tenant_id = $2`,
		id, e.tenant).Scan(&s.Methodology, &inh, &res); err != nil {
		t.Fatalf("read risk %s: %v", id, err)
	}
	if err := json.Unmarshal(inh, &s.Inherent); err != nil {
		t.Fatalf("decode inherent %s: %v", inh, err)
	}
	if err := json.Unmarshal(res, &s.Residual); err != nil {
		t.Fatalf("decode residual %s: %v", res, err)
	}
	return s
}

func scoreOf(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	if raw == nil {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decode score %s: %v", raw, err)
	}
	return m
}

func TestRiskImport_ScoresPerMethodology(t *testing.T) {
	grid := func(l, i float64) map[string]any { return map[string]any{"likelihood": l, "impact": i} }
	fair := func(lef, lm float64) map[string]any {
		return map[string]any{"loss_event_frequency": lef, "loss_magnitude": lm, "annualized_loss_exposure": lef * lm}
	}
	gridCSV := "Risk ID,Title,Category,Inherent Likelihood,Inherent Impact,Residual Likelihood,Residual Impact\n" +
		"S-1,Scored,operational,4,5,2,3\n" +
		"S-2,Unscored,operational,,,,\n" +
		"S-3,Out of range,operational,6,2,,\n"
	cases := []struct {
		name, methodology, csv, wantMethodology string
		// scored is S-1's inherent and residual, staged and on the risk.
		scoredInherent, scoredResidual map[string]any
	}{
		{"qualitative 5x5", "qualitative_5x5", gridCSV, "qualitative_5x5", grid(4, 5), grid(2, 3)},
		{"nist 800-30", "nist_800_30", gridCSV, "nist_800_30", grid(4, 5), grid(2, 3)},
		{"no methodology is nist 800-30", "", gridCSV, "nist_800_30", grid(4, 5), grid(2, 3)},
		{
			name:        "fair",
			methodology: "fair",
			csv: "Risk ID,Title,Category,Loss Event Frequency,Loss Magnitude,Residual LEF,Residual LM\n" +
				"S-1,Scored,operational,0.5,250000,0.1,100000\n" +
				"S-2,Unscored,operational,,,,\n" +
				"S-3,Negative,operational,-1,1000,,\n",
			wantMethodology: "fair",
			scoredInherent:  fair(0.5, 250000),
			scoredResidual:  fair(0.1, 100000),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			status, raw := uploadWithMethodology(t, e, e.grcJWT, tc.methodology, []byte(tc.csv))
			if status != http.StatusCreated {
				t.Fatalf("upload = %d; body=%s", status, raw)
			}
			b := decode[riskimport.Batch](t, raw)
			if b.Methodology != tc.wantMethodology || b.StagedCount != 2 || b.RejectedCount != 1 {
				t.Fatalf("batch methodology=%q staged=%d rejected=%d, want %q 2/1 (rejected %+v)",
					b.Methodology, b.StagedCount, b.RejectedCount, tc.wantMethodology, b.Rejected)
			}
			if r := b.Rejected[0]; r.ExternalRef != "S-3" || r.Reason != riskimport.RejectInvalidScore || r.Detail == "" {
				t.Errorf("rejected = %+v, want S-3 invalid_score with a detail", r)
			}

			// The batch read back shows the methodology and each row's scores.
			status, raw = call(t, e, http.MethodGet, e.grcJWT, "/v1/risks/imports/"+b.ID, nil)
			if status != http.StatusOK {
				t.Fatalf("GET batch = %d; body=%s", status, raw)
			}
			read := decode[riskimport.Batch](t, raw)
			if read.Methodology != tc.wantMethodology {
				t.Errorf("GET batch methodology = %q, want %q", read.Methodology, tc.wantMethodology)
			}
			scored, unscored := stagedByRef(t, read, "S-1"), stagedByRef(t, read, "S-2")
			if got := scoreOf(t, scored.InherentScore); !reflect.DeepEqual(got, tc.scoredInherent) {
				t.Errorf("staged S-1 inherent = %v, want %v", got, tc.scoredInherent)
			}
			if got := scoreOf(t, scored.ResidualScore); !reflect.DeepEqual(got, tc.scoredResidual) {
				t.Errorf("staged S-1 residual = %v, want %v", got, tc.scoredResidual)
			}
			if unscored.InherentScore != nil || unscored.ResidualScore != nil {
				t.Errorf("staged S-2 = %s / %s, want unscored", unscored.InherentScore, unscored.ResidualScore)
			}

			// Approval creates each risk with the batch methodology and
			// the staged scores; the unscored row is an unscored risk.
			status, raw = approve(t, e, e.apprJWT, b.ID)
			if status != http.StatusOK {
				t.Fatalf("approve = %d; body=%s", status, raw)
			}
			res := decode[riskimport.ApproveResult](t, raw)
			if res.ApprovedCount != 2 || len(res.Skipped) != 0 {
				t.Fatalf("approve result = %+v", res)
			}
			ids := map[string]string{}
			for _, d := range res.Approved {
				ids[d.ExternalRef] = d.RiskID
			}
			got := readStoredRisk(t, e, ids["S-1"])
			if got.Methodology != tc.wantMethodology || !reflect.DeepEqual(got.Inherent, tc.scoredInherent) ||
				!reflect.DeepEqual(got.Residual, tc.scoredResidual) {
				t.Errorf("S-1 risk = %+v, want %s %v / %v", got, tc.wantMethodology, tc.scoredInherent, tc.scoredResidual)
			}
			got = readStoredRisk(t, e, ids["S-2"])
			if got.Methodology != tc.wantMethodology || len(got.Inherent) != 0 || len(got.Residual) != 0 {
				t.Errorf("S-2 risk = %+v, want %s unscored", got, tc.wantMethodology)
			}
		})
	}
}

func TestRiskImport_MethodologyRefused(t *testing.T) {
	e := newEnv(t)
	csv := []byte("Title,Category\nA,operational\n")
	for _, m := range []string{"cis_ram", "iso_27005", "five_by_five"} {
		status, raw := uploadWithMethodology(t, e, e.grcJWT, m, csv)
		if status != http.StatusBadRequest || !strings.Contains(string(raw), "methodology") {
			t.Errorf("methodology %q: upload = %d %s, want 400 naming the methodology", m, status, raw)
		}
	}

	// Two methodology fields: a batch has one.
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, _ := mw.CreateFormFile("file", "register.csv")
	_, _ = part.Write(csv)
	_ = mw.WriteField("methodology", "fair")
	_ = mw.WriteField("methodology", "nist_800_30")
	_ = mw.Close()
	req, _ := http.NewRequest(http.MethodPost, e.server.URL+"/v1/risks/imports", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+e.grcJWT)
	if status, raw := do(t, req); status != http.StatusBadRequest {
		t.Errorf("two methodologies: upload = %d %s, want 400", status, raw)
	}

	if n := countBatches(t, e); n != 0 {
		t.Errorf("refused uploads staged %d batches, want 0", n)
	}
}

func countBatches(t *testing.T, e env) int {
	t.Helper()
	var n int
	if err := e.admin.QueryRow(context.Background(),
		`SELECT count(*) FROM risk_import_batches WHERE tenant_id = $1`, e.tenant).Scan(&n); err != nil {
		t.Fatalf("count batches: %v", err)
	}
	return n
}
