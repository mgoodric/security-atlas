//go:build integration

// #1814 — exports, the dashboard snapshot and the board pack print the
// organisation's category names (a migrate step of #1745). One tenant, one
// mitigate risk in `privacy`, renamed to "Data Protection" through the
// category API. After the rename:
//
//   - the risk CSV export carries the slug in `category` and the new name
//     in `category_name`;
//   - the dashboard snapshot export's risks panel carries both;
//   - a board pack generated after the rename prints the new name in its
//     top-risks narrative and section data.

package risks_test

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/dbtest"
)

// rawCall issues one request and returns the status and the raw body.
func rawCall(t *testing.T, env taxEnv, method, path string, payload any) (int, []byte) {
	t.Helper()
	var buf bytes.Buffer
	if payload != nil {
		_ = json.NewEncoder(&buf).Encode(payload)
	}
	req, _ := http.NewRequest(method, env.url+path, &buf)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+env.admin)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("%s %s: read body: %v", method, path, err)
	}
	return resp.StatusCode, body
}

func TestRiskCategoryExports_ShowRenamedCategory(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	ctx := context.Background()
	tenant := dbtest.SeedTenant(t, admin,
		"board_packs", "me_audit_log", "risk_audit_log", "risks",
		"taxonomy_audit_log", "org_risk_categories",
		"feature_flag_audit_log", "feature_flags")
	if _, err := admin.Exec(ctx, `SELECT seed_risk_categories($1)`, tenant); err != nil {
		t.Fatalf("seed categories: %v", err)
	}
	if _, err := admin.Exec(ctx, `
		INSERT INTO feature_flags (tenant_id, flag_key, enabled, description, category, last_changed_by, last_changed_at)
		VALUES ($1, 'board.reporting', TRUE, '', 'board', 'integration-test', now())`, tenant); err != nil {
		t.Fatalf("enable board.reporting: %v", err)
	}
	riskID := uuid.New()
	if _, err := admin.Exec(ctx, `
		INSERT INTO risks (
			id, tenant_id, title, description, category, methodology,
			inherent_score, treatment, treatment_owner, residual_score, themes
		) VALUES ($1, $2, 'Customer data leak', '', 'privacy', 'qualitative_5x5',
		          '{"likelihood":4,"impact":4}'::jsonb, 'mitigate', 'owner', '{}'::jsonb, '{}')
	`, riskID, tenant); err != nil {
		t.Fatalf("seed risk: %v", err)
	}

	env := taxServer(t, dbtest.NewAppPool(t), tenant)
	if status, body := taxCall(t, env, http.MethodPatch, "/v1/risks/categories/privacy", env.admin,
		map[string]any{"name": "Data Protection"}); status != http.StatusOK {
		t.Fatalf("rename privacy: status %d (%v)", status, body)
	}

	t.Run("risk CSV export", func(t *testing.T) {
		status, body := rawCall(t, env, http.MethodGet, "/v1/risks/export?format=csv", nil)
		if status != http.StatusOK {
			t.Fatalf("GET /v1/risks/export: status %d (%s)", status, body)
		}
		records, err := csv.NewReader(bytes.NewReader(body)).ReadAll()
		if err != nil {
			t.Fatalf("parse CSV: %v", err)
		}
		if len(records) != 2 {
			t.Fatalf("CSV has %d records, want header + 1 row:\n%s", len(records), body)
		}
		header, row := records[0], records[1]
		slugCol, nameCol := slices.Index(header, "category"), slices.Index(header, "category_name")
		if slugCol < 0 || nameCol < 0 {
			t.Fatalf("CSV header lacks category or category_name: %v", header)
		}
		if row[slugCol] != "privacy" || row[nameCol] != "Data Protection" {
			t.Errorf("CSV category = %q / %q, want privacy / Data Protection", row[slugCol], row[nameCol])
		}
	})

	t.Run("dashboard snapshot export", func(t *testing.T) {
		status, body := rawCall(t, env, http.MethodGet, "/v1/dashboard/export?format=json", nil)
		if status != http.StatusOK {
			t.Fatalf("GET /v1/dashboard/export: status %d (%s)", status, body)
		}
		var snap struct {
			Panels struct {
				Risks []struct {
					ID           string `json:"id"`
					Category     string `json:"category"`
					CategoryName string `json:"category_name"`
				} `json:"risks"`
			} `json:"panels"`
		}
		if err := json.Unmarshal(body, &snap); err != nil {
			t.Fatalf("decode snapshot: %v", err)
		}
		if len(snap.Panels.Risks) != 1 || snap.Panels.Risks[0].ID != riskID.String() {
			t.Fatalf("snapshot risks panel = %+v, want the one seeded risk", snap.Panels.Risks)
		}
		if r := snap.Panels.Risks[0]; r.Category != "privacy" || r.CategoryName != "Data Protection" {
			t.Errorf("snapshot category = %q / %q, want privacy / Data Protection", r.Category, r.CategoryName)
		}
	})

	t.Run("board pack narrative", func(t *testing.T) {
		periodEnd := time.Now().UTC().Format("2006-01-02")
		status, body := rawCall(t, env, http.MethodPost, "/v1/board-packs",
			map[string]any{"period_end": periodEnd})
		if status != http.StatusCreated {
			t.Fatalf("POST /v1/board-packs: status %d (%s)", status, body)
		}
		var pack struct {
			Content struct {
				Sections map[string]struct {
					TemplatedText string `json:"templated_text"`
					Data          struct {
						TopRisks []struct {
							Category     string `json:"category"`
							CategoryName string `json:"category_name"`
						} `json:"top_risks"`
					} `json:"data"`
				} `json:"sections"`
			} `json:"content"`
			NarrativeMd string `json:"narrative_md"`
		}
		if err := json.Unmarshal(body, &pack); err != nil {
			t.Fatalf("decode pack: %v", err)
		}
		sec := pack.Content.Sections["top_risks"]
		if len(sec.Data.TopRisks) != 1 {
			t.Fatalf("top_risks data = %+v, want the one seeded risk", sec.Data.TopRisks)
		}
		if r := sec.Data.TopRisks[0]; r.Category != "privacy" || r.CategoryName != "Data Protection" {
			t.Errorf("pack top risk category = %q / %q, want privacy / Data Protection", r.Category, r.CategoryName)
		}
		if !strings.Contains(sec.TemplatedText, "(Data Protection, treatment:") {
			t.Errorf("top_risks narrative does not print the renamed category: %q", sec.TemplatedText)
		}
		if !strings.Contains(pack.NarrativeMd, "Data Protection") {
			t.Errorf("pack narrative_md does not print the renamed category:\n%s", pack.NarrativeMd)
		}
	})
}
