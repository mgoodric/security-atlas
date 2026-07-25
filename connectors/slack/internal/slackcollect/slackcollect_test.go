package slackcollect

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// --- fakes ---

type fakeMembersAPI struct {
	pages [][]Member
	next  []string
	calls int
	err   error
}

func (f *fakeMembersAPI) ListMembers(_ context.Context, _ string) ([]Member, string, error) {
	if f.err != nil {
		return nil, "", f.err
	}
	i := f.calls
	f.calls++
	if i >= len(f.pages) {
		return nil, "", nil
	}
	next := ""
	if i < len(f.next) {
		next = f.next[i]
	}
	return f.pages[i], next, nil
}

type fakeAuditAPI struct {
	pages [][]AuditEvent
	next  []string
	calls int
	err   error
}

func (f *fakeAuditAPI) ListAuditEvents(_ context.Context, _ string) ([]AuditEvent, string, error) {
	if f.err != nil {
		return nil, "", f.err
	}
	i := f.calls
	f.calls++
	if i >= len(f.pages) {
		return nil, "", nil
	}
	next := ""
	if i < len(f.next) {
		next = f.next[i]
	}
	return f.pages[i], next, nil
}

type fakeRetentionAPI struct {
	s   RetentionSettings
	err error
}

func (f *fakeRetentionAPI) GetRetention(_ context.Context) (RetentionSettings, error) {
	return f.s, f.err
}

// --- scoreMember ---

func TestScoreMember(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		in     Member
		want   Result
		reason bool
	}{
		{"active-with-2fa-pass", Member{UserID: "U1", Has2FA: true}, ResultPass, false},
		{"active-no-2fa-fail", Member{UserID: "U2", Has2FA: false}, ResultFail, true},
		{"deleted-account-pass", Member{UserID: "U3", Deleted: true, Has2FA: false}, ResultPass, false},
		{"bot-inconclusive", Member{UserID: "U4", IsBot: true}, ResultInconclusive, true},
		{"deleted-takes-precedence-over-bot", Member{UserID: "U5", Deleted: true, IsBot: true}, ResultPass, false},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := scoreMember(tc.in)
			if got.Result != tc.want {
				t.Errorf("Result = %q; want %q", got.Result, tc.want)
			}
			if tc.reason && got.Reason == "" {
				t.Error("expected a non-empty Reason")
			}
			if !tc.reason && got.Reason != "" {
				t.Errorf("expected empty Reason, got %q", got.Reason)
			}
		})
	}
}

// --- scoreRetention ---

func TestScoreRetention(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   RetentionSettings
		want Result
	}{
		{"finite-enabled-pass", RetentionSettings{RetentionEnabled: true, MessagesRetentionDays: 90}, ResultPass},
		{"retain-forever-fail", RetentionSettings{RetentionEnabled: false, MessagesRetentionDays: 0}, ResultFail},
		{"enabled-but-zero-days-fail", RetentionSettings{RetentionEnabled: true, MessagesRetentionDays: 0}, ResultFail},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := scoreRetention(tc.in)
			if got.Result != tc.want {
				t.Errorf("Result = %q; want %q", got.Result, tc.want)
			}
			if got.Result == ResultFail && got.Reason == "" {
				t.Error("fail verdict should carry a reason")
			}
		})
	}
}

// --- CollectMembers pagination ---

func TestCollectMembers_Paginates(t *testing.T) {
	t.Parallel()
	api := &fakeMembersAPI{
		pages: [][]Member{
			{{UserID: "U1", Has2FA: true}, {UserID: "U2"}},
			{{UserID: "U3", Has2FA: true}},
		},
		next: []string{"cursor-2", ""},
	}
	got, err := CollectMembers(context.Background(), api)
	if err != nil {
		t.Fatalf("CollectMembers: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d; want 3", len(got))
	}
	if api.calls != 2 {
		t.Errorf("calls = %d; want 2", api.calls)
	}
	if got[1].Result != ResultFail {
		t.Errorf("U2 (no 2fa) Result = %q; want fail", got[1].Result)
	}
}

func TestCollectMembers_PropagatesError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("boom")
	_, err := CollectMembers(context.Background(), &fakeMembersAPI{err: sentinel})
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v; want sentinel chain", err)
	}
}

func TestCollectMembers_MaxPagesGuard(t *testing.T) {
	t.Parallel()
	// Every page returns a non-empty next cursor → would loop forever without
	// the MaxPages guard (threat-model D — denial of service).
	pages := make([][]Member, MaxPages+1)
	next := make([]string, MaxPages+1)
	for i := range pages {
		pages[i] = []Member{{UserID: "U", Has2FA: true}}
		next[i] = "always-more"
	}
	_, err := CollectMembers(context.Background(), &fakeMembersAPI{pages: pages, next: next})
	if err == nil || !strings.Contains(err.Error(), "MaxPages") {
		t.Fatalf("err = %v; want MaxPages bound error", err)
	}
}

// --- CollectAuditEvents ---

func TestCollectAuditEvents_Paginates(t *testing.T) {
	t.Parallel()
	api := &fakeAuditAPI{
		pages: [][]AuditEvent{
			{{ID: "e1", Action: "user_login"}},
			{{ID: "e2", Action: "role_change"}},
		},
		next: []string{"c2", ""},
	}
	got, err := CollectAuditEvents(context.Background(), api)
	if err != nil {
		t.Fatalf("CollectAuditEvents: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d; want 2", len(got))
	}
}

// --- CollectRetention ---

func TestCollectRetention(t *testing.T) {
	t.Parallel()
	got, err := CollectRetention(context.Background(), &fakeRetentionAPI{
		s: RetentionSettings{RetentionEnabled: true, MessagesRetentionDays: 30},
	})
	if err != nil {
		t.Fatalf("CollectRetention: %v", err)
	}
	if got.Result != ResultPass {
		t.Errorf("Result = %q; want pass", got.Result)
	}
}

// bannedContentTokens are the WORD tokens that denote message-body /
// channel-history content the connector must never carry. Matching is on
// split identifier words (not naive substring) so a legitimate metadata
// field like "is_admin" (word "admin") or "messages_retention_days" (words
// "messages","retention","days") is NOT a false positive — only a field
// whose own word is "body", "text", "history", etc. trips the guard.
var bannedContentTokens = map[string]bool{
	"body": true, "text": true, "content": true, "history": true,
	"dm": true, "conversation": true, "conversations": true,
	"thread": true, "threads": true, "reply": true, "replies": true,
	"attachment": true, "attachments": true, "blocks": true,
}

// splitIdentifierWords lowercases and splits a CamelCase Go field name into
// its constituent words (IsAdmin -> [is admin], MessagesRetentionDays ->
// [messages retention days]).
func splitIdentifierWords(name string) []string {
	var words []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			words = append(words, strings.ToLower(cur.String()))
			cur.Reset()
		}
	}
	for _, r := range name {
		if r >= 'A' && r <= 'Z' {
			flush()
		}
		if r == '_' || r == '-' {
			flush()
			continue
		}
		cur.WriteRune(r)
	}
	flush()
	return words
}

// TestNoMessageContentField is the load-bearing over-collection guard
// (slice 443 threat-model I / AC-11). It reflects over EVERY field of the
// three evidence structs and fails the build if any field's WORD tokens
// denote message-body / channel-history content. The connector must emit
// membership/admin/retention METADATA only.
func TestNoMessageContentField(t *testing.T) {
	t.Parallel()
	for _, typ := range []reflect.Type{
		reflect.TypeOf(Member{}),
		reflect.TypeOf(AuditEvent{}),
		reflect.TypeOf(RetentionSettings{}),
	} {
		for i := 0; i < typ.NumField(); i++ {
			for _, w := range splitIdentifierWords(typ.Field(i).Name) {
				if bannedContentTokens[w] {
					t.Errorf("%s.%s contains banned over-collection word %q — the connector must emit metadata only, never message content (threat-model I)",
						typ.Name(), typ.Field(i).Name, w)
				}
			}
		}
	}
}
