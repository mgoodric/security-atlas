// calleruser_test.go — #2183: CallerUserID is the one resolver from a
// credential to its users(id), and nothing else parses cred.UserID raw.
//
// Pure-Go (slice-353 Q-2 fast loop). A local-login / PKCE / device-grant JWT
// bridges Subject "user:<uuid>" into cred.UserID; a raw uuid.Parse of that
// fails, so before #2055 / #2183 a signed-in person read as a machine caller.

package credstore

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestCallerUserID_PerCredentialType(t *testing.T) {
	t.Parallel()
	userID := uuid.New()
	keyUUID := uuid.NewString()
	cases := []struct {
		name   string
		c      Credential
		want   uuid.UUID
		wantOK bool
	}{
		{"user JWT (local login, PKCE, device grant)", Credential{ID: "jwt:abc", UserID: "user:" + userID.String()}, userID, true},
		{"key bound to a user", Credential{ID: "key_abc", UserID: userID.String()}, userID, true},
		{"unbound key (own credential id)", Credential{ID: "key_abc", UserID: "key_abc"}, uuid.Nil, false},
		// A uuid-shaped credential id is still the key's own id, not a user.
		{"unbound key with a uuid-shaped id", Credential{ID: keyUUID, UserID: keyUUID}, uuid.Nil, false},
		{"bootstrap admin", Credential{ID: "key_boot", UserID: "bootstrap-admin"}, uuid.Nil, false},
		{"OAuth client (machine token)", Credential{ID: "jwt:m", UserID: "oauth_client:ci-runner"}, uuid.Nil, false},
		{"user: prefix on a non-uuid", Credential{ID: "jwt:x", UserID: "user:not-a-uuid"}, uuid.Nil, false},
		{"nil uuid", Credential{ID: "jwt:n", UserID: "user:" + uuid.Nil.String()}, uuid.Nil, false},
		{"empty", Credential{}, uuid.Nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := CallerUserID(tc.c)
			if ok != tc.wantOK || got != tc.want {
				t.Errorf("CallerUserID(%+v) = (%s, %v); want (%s, %v)", tc.c, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestSubjectUserID(t *testing.T) {
	t.Parallel()
	u := uuid.New()
	for in, want := range map[string]bool{
		"user:" + u.String():        true,
		u.String():                  true,
		"key_abc":                   false,
		"bootstrap-admin":           false,
		"oauth_client:ci-runner":    false,
		"user:" + uuid.Nil.String(): false,
		"":                          false,
	} {
		got, ok := SubjectUserID(in)
		if ok != want || (ok && got != u) {
			t.Errorf("SubjectUserID(%q) = (%s, %v); want ok=%v", in, got, ok, want)
		}
	}
}

// rawCredUserIDParse matches a uuid parse whose argument reads a credential's
// UserID, directly or through jwtmw.SubjectUserID / a local prefix-stripper:
// uuid.Parse(cred.UserID), uuid.Parse(jwtmw.SubjectUserID(cred.UserID)),
// uuid.MustParse(rec.Cred.UserID). Credential variables are named cred / Cred
// by convention across internal/ and cmd/.
var rawCredUserIDParse = regexp.MustCompile(`uuid\.(Must)?Parse\([^;\n]*\b[Cc]red\.UserID\b`)

// TestNoRawCredUserIDParse is the #2183 grep guard: production code resolves
// a caller's users(id) through CallerUserID, never by parsing cred.UserID.
// A raw parse treats a "user:<uuid>" JWT subject as a non-user and an unbound
// key whose id happens to parse as a user.
func TestNoRawCredUserIDParse(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "..", "..")
	var hits []string
	for _, dir := range []string{"internal", "cmd", "pkg"} {
		base := filepath.Join(root, dir)
		if _, err := os.Stat(base); err != nil {
			continue
		}
		err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for i, line := range strings.Split(string(src), "\n") {
				if rawCredUserIDParse.MatchString(line) {
					rel, _ := filepath.Rel(root, path)
					hits = append(hits, rel+":"+strconv.Itoa(i+1)+": "+strings.TrimSpace(line))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", base, err)
		}
	}
	if len(hits) > 0 {
		t.Errorf("raw parse of a credential's UserID; use credstore.CallerUserID (#2183):\n  %s", strings.Join(hits, "\n  "))
	}
}

func TestRawCredUserIDParse_Pattern(t *testing.T) {
	t.Parallel()
	for _, line := range []string{
		`enteredBy, err := uuid.Parse(cred.UserID)`,
		`actor, err := uuid.Parse(jwtmw.SubjectUserID(cred.UserID))`,
		`if u, err := uuid.Parse(subjectUserID(cred.UserID)); err == nil {`,
		`if u, uerr := uuid.Parse(rec.Cred.UserID); uerr == nil {`,
		`id := uuid.MustParse(cred.UserID)`,
	} {
		if !rawCredUserIDParse.MatchString(line) {
			t.Errorf("guard misses %q", line)
		}
	}
	for _, line := range []string{
		`id, err := uuid.Parse(strings.TrimSpace(req.UserID))`,
		`if u, ok := credstore.CallerUserID(cred); ok {`,
		`tenantUUID, err := uuid.Parse(cred.TenantID)`,
		`userID, err := uuid.Parse(caller.UserID)`,
	} {
		if rawCredUserIDParse.MatchString(line) {
			t.Errorf("guard flags %q", line)
		}
	}
}
