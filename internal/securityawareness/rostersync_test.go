package securityawareness

import (
	"errors"
	"testing"
)

func TestMapHRISPerson(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		in          HRISRosterRecord
		wantErr     error
		wantKey     string
		wantDisplay string
		wantEmail   *string
		wantActive  bool
	}{
		{
			name:        "active worker with email",
			in:          HRISRosterRecord{SourceHRIS: "rippling", WorkerID: "w-1", EmploymentStatus: "active", WorkEmail: "Alice@Example.test"},
			wantKey:     "rippling:w-1",
			wantDisplay: "Alice@Example.test",
			wantEmail:   ptr("Alice@Example.test"),
			wantActive:  true,
		},
		{
			name:        "terminated worker deactivates",
			in:          HRISRosterRecord{SourceHRIS: "bamboohr", WorkerID: "42", EmploymentStatus: "terminated", WorkEmail: "bob@example.test"},
			wantKey:     "bamboohr:42",
			wantDisplay: "bob@example.test",
			wantEmail:   ptr("bob@example.test"),
			wantActive:  false,
		},
		{
			name:        "terminated matches case-insensitively",
			in:          HRISRosterRecord{SourceHRIS: "rippling", WorkerID: "w-2", EmploymentStatus: "TERMINATED"},
			wantKey:     "rippling:w-2",
			wantDisplay: "rippling:w-2",
			wantActive:  false,
		},
		{
			name:        "on_leave stays active",
			in:          HRISRosterRecord{SourceHRIS: "bamboohr", WorkerID: "43", EmploymentStatus: "on_leave"},
			wantKey:     "bamboohr:43",
			wantDisplay: "bamboohr:43",
			wantActive:  true,
		},
		{
			name:        "unknown status stays active",
			in:          HRISRosterRecord{SourceHRIS: "rippling", WorkerID: "w-3", EmploymentStatus: "unknown"},
			wantKey:     "rippling:w-3",
			wantDisplay: "rippling:w-3",
			wantActive:  true,
		},
		{
			name:        "missing email falls back to key",
			in:          HRISRosterRecord{SourceHRIS: "rippling", WorkerID: "w-4", EmploymentStatus: "active", WorkEmail: "  "},
			wantKey:     "rippling:w-4",
			wantDisplay: "rippling:w-4",
			wantActive:  true,
		},
		{
			name:    "blank worker id rejected",
			in:      HRISRosterRecord{SourceHRIS: "rippling", WorkerID: "  ", EmploymentStatus: "active"},
			wantErr: ErrInvalidInput,
		},
		{
			name:    "blank vendor rejected",
			in:      HRISRosterRecord{SourceHRIS: "", WorkerID: "w-5", EmploymentStatus: "active"},
			wantErr: ErrInvalidInput,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := MapHRISPerson(tc.in)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("MapHRISPerson: %v", err)
			}
			if got.Source != PersonSourceHRIS {
				t.Errorf("Source = %q, want %q", got.Source, PersonSourceHRIS)
			}
			if got.SourcePersonID == nil || *got.SourcePersonID != tc.wantKey {
				t.Errorf("SourcePersonID = %v, want %q", got.SourcePersonID, tc.wantKey)
			}
			if got.DisplayName != tc.wantDisplay {
				t.Errorf("DisplayName = %q, want %q", got.DisplayName, tc.wantDisplay)
			}
			if (got.WorkEmail == nil) != (tc.wantEmail == nil) {
				t.Errorf("WorkEmail = %v, want %v", got.WorkEmail, tc.wantEmail)
			} else if got.WorkEmail != nil && *got.WorkEmail != *tc.wantEmail {
				t.Errorf("WorkEmail = %q, want %q", *got.WorkEmail, *tc.wantEmail)
			}
			if got.RecipientUserID != nil {
				t.Errorf("RecipientUserID = %q, want nil (HRIS payload carries no platform user)", *got.RecipientUserID)
			}
			if got.Active == nil || *got.Active != tc.wantActive {
				t.Errorf("Active = %v, want %v", got.Active, tc.wantActive)
			}
		})
	}
}

func TestMapSCIMPerson(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		in          SCIMRosterRecord
		wantErr     error
		wantDisplay string
		wantEmail   *string
		wantActive  bool
	}{
		{
			name:        "active user with display name",
			in:          SCIMRosterRecord{UserID: "9f6f9f34-0000-0000-0000-000000000001", DisplayName: "Carol Example", Email: "carol@example.test", Active: true},
			wantDisplay: "Carol Example",
			wantEmail:   ptr("carol@example.test"),
			wantActive:  true,
		},
		{
			name:        "deactivated user propagates",
			in:          SCIMRosterRecord{UserID: "9f6f9f34-0000-0000-0000-000000000002", DisplayName: "Dave Example", Email: "dave@example.test", Active: false},
			wantDisplay: "Dave Example",
			wantEmail:   ptr("dave@example.test"),
			wantActive:  false,
		},
		{
			name:        "blank display name falls back to email",
			in:          SCIMRosterRecord{UserID: "9f6f9f34-0000-0000-0000-000000000003", DisplayName: " ", Email: "erin@example.test", Active: true},
			wantDisplay: "erin@example.test",
			wantEmail:   ptr("erin@example.test"),
			wantActive:  true,
		},
		{
			name:        "no name and no email falls back to user id",
			in:          SCIMRosterRecord{UserID: "9f6f9f34-0000-0000-0000-000000000004", Active: true},
			wantDisplay: "9f6f9f34-0000-0000-0000-000000000004",
			wantActive:  true,
		},
		{
			name:    "blank user id rejected",
			in:      SCIMRosterRecord{UserID: "  ", DisplayName: "Nobody", Active: true},
			wantErr: ErrInvalidInput,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := MapSCIMPerson(tc.in)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("MapSCIMPerson: %v", err)
			}
			if got.Source != PersonSourceSCIM {
				t.Errorf("Source = %q, want %q", got.Source, PersonSourceSCIM)
			}
			if got.SourcePersonID == nil || *got.SourcePersonID != tc.in.UserID {
				t.Errorf("SourcePersonID = %v, want %q", got.SourcePersonID, tc.in.UserID)
			}
			if got.DisplayName != tc.wantDisplay {
				t.Errorf("DisplayName = %q, want %q", got.DisplayName, tc.wantDisplay)
			}
			if (got.WorkEmail == nil) != (tc.wantEmail == nil) {
				t.Errorf("WorkEmail = %v, want %v", got.WorkEmail, tc.wantEmail)
			} else if got.WorkEmail != nil && *got.WorkEmail != *tc.wantEmail {
				t.Errorf("WorkEmail = %q, want %q", *got.WorkEmail, *tc.wantEmail)
			}
			if got.RecipientUserID == nil || *got.RecipientUserID != tc.in.UserID {
				t.Errorf("RecipientUserID = %v, want %q (SCIM person is a platform user)", got.RecipientUserID, tc.in.UserID)
			}
			if got.Active == nil || *got.Active != tc.wantActive {
				t.Errorf("Active = %v, want %v", got.Active, tc.wantActive)
			}
		})
	}
}

func ptr(v string) *string { return &v }
