package gcpcollect

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// --- fakes ---

type fakeIAMAPI struct {
	pages [][]IAMBinding
	err   error
	calls int
}

func (f *fakeIAMAPI) ListIAMBindings(_ context.Context, _ string) ([]IAMBinding, string, error) {
	if f.err != nil {
		return nil, "", f.err
	}
	if f.calls >= len(f.pages) {
		return nil, "", nil
	}
	page := f.pages[f.calls]
	f.calls++
	next := ""
	if f.calls < len(f.pages) {
		next = "more"
	}
	return page, next, nil
}

type fakeStorageAPI struct {
	pages [][]StorageBucket
	err   error
	calls int
}

func (f *fakeStorageAPI) ListBuckets(_ context.Context, _ string) ([]StorageBucket, string, error) {
	if f.err != nil {
		return nil, "", f.err
	}
	if f.calls >= len(f.pages) {
		return nil, "", nil
	}
	page := f.pages[f.calls]
	f.calls++
	next := ""
	if f.calls < len(f.pages) {
		next = "more"
	}
	return page, next, nil
}

// --- scoreBinding ---

func TestScoreBinding(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   IAMBinding
		want Result
	}{
		{"disabled service account -> pass", IAMBinding{IsServiceAcc: true, Disabled: true}, ResultPass},
		{"live privileged -> inconclusive", IAMBinding{Role: "roles/owner", IsPrivileged: true}, ResultInconclusive},
		{"live user binding -> inconclusive", IAMBinding{Member: "user:a@b.com", MemberType: "user"}, ResultInconclusive},
		{"live enabled service account -> inconclusive", IAMBinding{IsServiceAcc: true, Disabled: false}, ResultInconclusive},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := scoreBinding(tc.in)
			if got.Result != tc.want {
				t.Errorf("Result = %q; want %q", got.Result, tc.want)
			}
			if got.Result != ResultPass && got.Reason == "" {
				t.Error("inconclusive binding should carry a reason")
			}
		})
	}
}

func TestCollectIAMBindings(t *testing.T) {
	t.Parallel()
	api := &fakeIAMAPI{pages: [][]IAMBinding{
		{{Member: "user:a@b.com", MemberType: "user", Role: "roles/viewer"}},
		{{Member: "serviceAccount:svc@p.iam", MemberType: "serviceAccount", IsServiceAcc: true, Disabled: true, Role: "roles/owner"}},
	}}
	got, err := CollectIAMBindings(context.Background(), api)
	if err != nil {
		t.Fatalf("CollectIAMBindings: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d; want 2", len(got))
	}
	if got[0].Result != ResultInconclusive {
		t.Errorf("binding[0].Result = %q; want inconclusive", got[0].Result)
	}
	if got[1].Result != ResultPass {
		t.Errorf("binding[1].Result = %q; want pass (disabled SA)", got[1].Result)
	}
}

func TestCollectIAMBindings_Error(t *testing.T) {
	t.Parallel()
	_, err := CollectIAMBindings(context.Background(), &fakeIAMAPI{err: errors.New("boom")})
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- scoreBucket ---

func TestScoreBucket(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   StorageBucket
		want Result
	}{
		{"enforced + uniform -> pass", StorageBucket{PublicAccessFlag: "enforced", UniformAccess: true}, ResultPass},
		{"not enforced -> fail", StorageBucket{PublicAccessFlag: "inherited", UniformAccess: true}, ResultFail},
		{"unspecified -> fail", StorageBucket{PublicAccessFlag: "unspecified", UniformAccess: true}, ResultFail},
		{"enforced but per-object ACLs -> fail", StorageBucket{PublicAccessFlag: "enforced", UniformAccess: false}, ResultFail},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := scoreBucket(tc.in)
			if got.Result != tc.want {
				t.Errorf("Result = %q; want %q", got.Result, tc.want)
			}
			if got.Result == ResultFail && got.Reason == "" {
				t.Error("failing bucket should carry a reason")
			}
		})
	}
}

func TestCollectBuckets(t *testing.T) {
	t.Parallel()
	api := &fakeStorageAPI{pages: [][]StorageBucket{
		{{Name: "good", PublicAccessFlag: "enforced", UniformAccess: true}},
		{{Name: "bad", PublicAccessFlag: "inherited", UniformAccess: true}},
	}}
	got, err := CollectBuckets(context.Background(), api)
	if err != nil {
		t.Fatalf("CollectBuckets: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d; want 2", len(got))
	}
	if got[0].Result != ResultPass || got[1].Result != ResultFail {
		t.Errorf("results = %q,%q; want pass,fail", got[0].Result, got[1].Result)
	}
}

func TestCollectBuckets_Error(t *testing.T) {
	t.Parallel()
	_, err := CollectBuckets(context.Background(), &fakeStorageAPI{err: errors.New("boom")})
	if err == nil {
		t.Fatal("expected error")
	}
}

// bannedContentTokens are the WORD tokens that denote stored-object content /
// secret-material the connector must never carry (slice 442 threat-model I).
// Matching is on split identifier words (not naive substring) so a legitimate
// metadata field like "default_kms_key_name" (words "default","kms","key",
// "name") is allowed — "key" here is the CMEK key NAME, never a key VALUE —
// while a field named "object", "blob", "body", "secret", or "credential"
// trips the guard.
var bannedContentTokens = map[string]bool{
	"object": true, "objects": true, "blob": true, "blobs": true,
	"body": true, "content": true, "contents": true, "data": true,
	"secret": true, "secrets": true, "credential": true, "credentials": true,
	"password": true, "token": true, "acl": true, "acls": true,
}

// splitIdentifierWords lowercases and splits a CamelCase / snake_case Go
// field name into its constituent words (IsServiceAcc -> [is service acc],
// DefaultKMSKeyName -> [default kms key name]).
func splitIdentifierWords(name string) []string {
	var words []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			words = append(words, strings.ToLower(cur.String()))
			cur.Reset()
		}
	}
	prevUpper := false
	runes := []rune(name)
	for i, r := range runes {
		isUpper := r >= 'A' && r <= 'Z'
		// Start a new word at a lower->upper boundary, but keep runs of
		// upper-case (acronyms like KMS) together until the next lower-case.
		if isUpper && !prevUpper {
			flush()
		}
		if isUpper && prevUpper && i+1 < len(runes) && runes[i+1] >= 'a' && runes[i+1] <= 'z' {
			flush()
		}
		if r == '_' || r == '-' {
			flush()
			prevUpper = false
			continue
		}
		cur.WriteRune(r)
		prevUpper = isUpper
	}
	flush()
	return words
}

// TestNoObjectContentField is the load-bearing over-collection guard (slice
// 442 threat-model I / AC-10). It reflects over EVERY field of the two
// evidence structs and fails the build if any field's WORD tokens denote
// stored-object content / secret material. The connector must emit IAM-binding
// + bucket-CONFIGURATION metadata only.
func TestNoObjectContentField(t *testing.T) {
	t.Parallel()
	for _, typ := range []reflect.Type{
		reflect.TypeOf(IAMBinding{}),
		reflect.TypeOf(StorageBucket{}),
	} {
		for i := 0; i < typ.NumField(); i++ {
			for _, w := range splitIdentifierWords(typ.Field(i).Name) {
				if bannedContentTokens[w] {
					t.Errorf("%s.%s contains banned over-collection word %q — the connector must emit config/binding metadata only, never object content or secret material (threat-model I)",
						typ.Name(), typ.Field(i).Name, w)
				}
			}
		}
	}
}

// TestSplitIdentifierWords sanity-checks the word splitter so the guard above
// is trustworthy: an acronym field name must split into the right words and a
// legitimate "key name" must NOT collapse into the banned-word space.
func TestSplitIdentifierWords(t *testing.T) {
	t.Parallel()
	cases := map[string][]string{
		"DefaultKMSKeyName": {"default", "kms", "key", "name"},
		"IsServiceAcc":      {"is", "service", "acc"},
		"PublicAccessFlag":  {"public", "access", "flag"},
		"RetentionSeconds":  {"retention", "seconds"},
	}
	for in, want := range cases {
		got := splitIdentifierWords(in)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("splitIdentifierWords(%q) = %v; want %v", in, got, want)
		}
	}
}
