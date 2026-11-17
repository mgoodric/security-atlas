//go:build integration

// #2446 integration tests. Whoever creates or edits a draft is one of its
// authors: they cannot approve it, whoever submitted it, unless the
// organisation allows self-approval, and then the approval is flagged.
// Each edit appends a draft_edited policy_audit_log row with the body's
// sha256; submit records the hash and the editor set. Linked controls
// must exist in the tenant, and create and edit bodies are size-limited.
// Real Postgres under RLS, driven through the full HTTP stack.

package policies_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func approvedFlag(t *testing.T, raw []byte) bool {
	t.Helper()
	var env struct {
		Policy struct {
			Status       string `json:"status"`
			SelfApproved bool   `json:"self_approved"`
		} `json:"policy"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("decode: %v body=%s", err, raw)
	}
	if env.Policy.Status != "approved" {
		t.Fatalf("status = %q, want approved", env.Policy.Status)
	}
	return env.Policy.SelfApproved
}

// The issue's scenario: the author writes the draft, a second person
// submits it, and the author approves. Refused with the setting off;
// allowed and flagged with it on.
func TestApprove_AuthorRefusedWhenAnotherSubmits(t *testing.T) {
	s := setup(t)
	allowSelfApproval(t, s, false)
	submitter := userJWT(t, s, seedUser(t, s.admin, s.tenant, "submitter@2446.test"))
	author := userJWT(t, s, s.adminUser)

	id := createDraftPolicy(t, s, "Author Then Other Submits", "1.0", nil)
	var editors []string
	if err := s.admin.QueryRow(context.Background(),
		`SELECT draft_editors FROM policies WHERE id = $1`, id).Scan(&editors); err != nil {
		t.Fatalf("read draft_editors: %v", err)
	}
	if len(editors) != 1 || editors[0] != s.adminUser.String() {
		t.Fatalf("draft_editors after create = %v, want [%s]", editors, s.adminUser)
	}

	s.do(t, http.MethodPatch, "/v1/policies/"+id+"/submit", nil, submitter, http.StatusOK)
	raw := s.do(t, http.MethodPatch, "/v1/policies/"+id+"/approve", nil, author, http.StatusForbidden)
	if !strings.Contains(string(raw), "author of this draft") {
		t.Fatalf("403 body = %s", raw)
	}

	allowSelfApproval(t, s, true)
	if !approvedFlag(t, s.do(t, http.MethodPatch, "/v1/policies/"+id+"/approve", nil, author, http.StatusOK)) {
		t.Fatal("author approval under self-approval is not flagged self_approved")
	}
	var action string
	if err := s.admin.QueryRow(context.Background(),
		`SELECT action FROM policy_audit_log WHERE policy_id = $1 AND new_state = 'approved'`, id).Scan(&action); err != nil {
		t.Fatalf("read approval audit row: %v", err)
	}
	if action != "self_approved" {
		t.Fatalf("approval audit action = %q, want self_approved", action)
	}
}

// A person who only edited the draft is an author too; a person who never
// touched it approves unflagged.
func TestApprove_EditorRefused(t *testing.T) {
	s := setup(t)
	allowSelfApproval(t, s, false)
	editorID := seedUser(t, s.admin, s.tenant, "editor@2446.test")
	editor := userJWT(t, s, editorID)

	id := createDraftPolicy(t, s, "Edited Policy", "1.0", nil)
	s.do(t, http.MethodPatch, "/v1/policies/"+id,
		editBody("Edited Policy", []string{s.ctrlID.String()}), editor, http.StatusOK)
	// A second edit by the same person does not duplicate them.
	s.do(t, http.MethodPatch, "/v1/policies/"+id,
		editBody("Edited Policy", []string{s.ctrlID.String()}), editor, http.StatusOK)

	var editors []string
	if err := s.admin.QueryRow(context.Background(),
		`SELECT draft_editors FROM policies WHERE id = $1`, id).Scan(&editors); err != nil {
		t.Fatalf("read draft_editors: %v", err)
	}
	if len(editors) != 2 || editors[0] != s.adminUser.String() || editors[1] != editorID.String() {
		t.Fatalf("draft_editors = %v, want [creator, editor]", editors)
	}

	// Each edit is one draft_edited row naming the editor and the body hash,
	// never the body itself.
	rows, err := s.admin.Query(context.Background(),
		`SELECT actor, body_sha256, encode(sha256(convert_to(p.body_md, 'UTF8')), 'hex')
		   FROM policy_audit_log l JOIN policies p ON p.id = l.policy_id
		  WHERE l.policy_id = $1 AND l.action = 'draft_edited'`, id)
	if err != nil {
		t.Fatalf("read draft_edited rows: %v", err)
	}
	n := 0
	for rows.Next() {
		var actor, hash, want string
		if err := rows.Scan(&actor, &hash, &want); err != nil {
			t.Fatal(err)
		}
		if actor != editorID.String() || hash != want {
			t.Errorf("draft_edited row: actor %q hash %q, want %s / %s", actor, hash, editorID, want)
		}
		n++
	}
	rows.Close()
	if n != 2 {
		t.Fatalf("draft_edited rows = %d, want 2", n)
	}

	s.do(t, http.MethodPatch, "/v1/policies/"+id+"/submit", nil, s.adminBear, http.StatusOK)
	var hash string
	var logged []string
	if err := s.admin.QueryRow(context.Background(),
		`SELECT body_sha256, editors FROM policy_audit_log WHERE policy_id = $1 AND action = 'submitted'`,
		id).Scan(&hash, &logged); err != nil {
		t.Fatalf("read submitted row: %v", err)
	}
	if len(hash) != 64 || len(logged) != 2 || logged[1] != editorID.String() {
		t.Fatalf("submitted row: hash %q editors %v", hash, logged)
	}

	s.do(t, http.MethodPatch, "/v1/policies/"+id+"/approve", nil, editor, http.StatusForbidden)
	if approvedFlag(t, s.do(t, http.MethodPatch, "/v1/policies/"+id+"/approve", nil, s.approverBear, http.StatusOK)) {
		t.Fatal("approval by a person who never edited the draft is flagged self_approved")
	}
}

// linked_control_ids must name controls of the caller's tenant, on create
// and on edit; a made-up id or another tenant's control is 422.
func TestLinkedControlsMustExistInTenant(t *testing.T) {
	s := setup(t)
	otherTenant := freshTenant(t, s.admin)
	foreign := seedControl(t, s.admin, otherTenant)

	create := func(link string) []byte {
		body, _ := json.Marshal(map[string]any{
			"title": "Linked " + link, "version": "1.0", "body_md": "# x",
			"owner_role": "a", "approver_role": "b",
			"linked_control_ids": []string{s.ctrlID.String(), link},
		})
		return body
	}
	for name, link := range map[string]string{
		"unknown":      uuid.NewString(),
		"other tenant": foreign.String(),
	} {
		t.Run(name, func(t *testing.T) {
			raw := s.do(t, http.MethodPost, "/v1/policies", create(link), s.adminBear, http.StatusUnprocessableEntity)
			if !strings.Contains(string(raw), "does not exist in this tenant") {
				t.Errorf("create 422 body = %s", raw)
			}
		})
	}
	// A duplicate of a real control is one control, not a missing one.
	s.do(t, http.MethodPost, "/v1/policies", create(s.ctrlID.String()), s.adminBear, http.StatusCreated)

	id := createDraftPolicy(t, s, "Edit Links Policy", "1.0", nil)
	s.do(t, http.MethodPatch, "/v1/policies/"+id,
		editBody("Edit Links Policy", []string{foreign.String()}), s.adminBear, http.StatusUnprocessableEntity)
	got := decodeDraft(t, s.do(t, http.MethodGet, "/v1/policies/"+id, nil, s.adminBear, http.StatusOK))
	if len(got.LinkedControlIDs) != 1 || got.LinkedControlIDs[0] != s.ctrlID.String() {
		t.Fatalf("refused edit changed the links to %v", got.LinkedControlIDs)
	}
}

// A create or edit body over 1 MiB is refused with 413 before decoding.
func TestPolicyBodySizeLimit(t *testing.T) {
	s := setup(t)
	huge := strings.Repeat("a", 1<<20+1)
	body, _ := json.Marshal(map[string]any{
		"title": "Huge", "version": "1.0", "body_md": huge,
		"owner_role": "a", "approver_role": "b",
	})
	s.do(t, http.MethodPost, "/v1/policies", body, s.adminBear, http.StatusRequestEntityTooLarge)

	id := createDraftPolicy(t, s, "Huge Edit Policy", "1.0", nil)
	edit, _ := json.Marshal(map[string]any{
		"title": "Huge Edit Policy", "body_md": huge,
		"owner_role": "a", "approver_role": "b",
	})
	s.do(t, http.MethodPatch, "/v1/policies/"+id, edit, s.adminBear, http.StatusRequestEntityTooLarge)
}
