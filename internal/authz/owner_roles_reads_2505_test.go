package authz_test

// #2505: drift guard for direct OwnerRoles reads. For a user's JWT session,
// credstore.Credential.OwnerRoles is the token's role claim, which outlives
// a revoke until the token expires (#2381). A gate reads roles through
// authz.TenantRoleLabels, HasAnyTenantRole, AckRoleLabels or the IsTenant*
// helpers, which count the live user_roles authzmw resolved.
//
// This test parses every non-test Go file under internal/ and cmd/, outside
// internal/authz and the generated internal/db/dbx, and records each
// function that reads a field named OwnerRoles (a selector x.OwnerRoles;
// a composite-literal key that sets it is not a read). That inventory must
// equal ownerRolesReads2505 exactly. A new read fails here until it is
// listed with the reason it is not a role gate. The scan cannot see types,
// so other structs' OwnerRoles fields are listed too.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// ownerRolesReads2505 is keyed by "<path under the repo root> <func>", with
// methods as "Recv.Name"; the value is why the read is not a role gate.
var ownerRolesReads2505 = map[string]string{
	"cmd/atlas-cli/cmd_oauth_migrate.go runMigrateAPIKey": "copies a legacy credential's label into an OAuth client registration; mints, does not gate",

	"internal/api/admincreds/http.go Handler.Issue": "the issue request body's owner_roles, written to the new key",
	"internal/api/admincreds/http.go Handler.List":  "lists each key's stored label; display only",

	"internal/api/credstore/credstore.go Credential.HasOwnerRole": "slice-011 attestation owner-role match against a control's free-text owner_role; not a tenant-role gate",
	"internal/api/credstore/credstore.go Store.Rotate":            "a rotated key's successor inherits its label",

	"internal/api/me/profile.go ProfileHandler.buildProfile": "echoes owner_roles on /v1/me; display only",
	"internal/api/me/profile.go profileWireFrom":             "echoes owner_roles on /v1/me; display only",

	"internal/auth/apikeystore/apikeystore.go Store.Rotate":      "a rotated key's successor inherits its label",
	"internal/auth/apikeystore/apikeystore.go Store.insert":      "persists a new key's label",
	"internal/auth/apikeystore/apikeystore.go credentialFromRow": "loads a key's stored label onto its credential",
	"internal/auth/apikeystore/persister.go Persister.insertRow": "persists an issued key's label",

	"internal/demoseed/writers.go writeRoleUsers": "seeds a demo user's api_keys label; fixture data",

	"internal/policy/acknowledgment.go AckStore.PendingForUser": "AckCaller.OwnerRoles, which policyacks fills from authz.AckRoleLabels",
	"internal/policy/acknowledgment.go rolesIntersect":          "AckCaller.OwnerRoles, which policyacks fills from authz.AckRoleLabels",

	// The `IsAdmin || IsApprover || len(OwnerRoles) > 0` read gates. They ARE
	// role gates that still count a session's claim; moving them to live
	// roles needs the integration fixtures that reach them to authenticate
	// with live roles first. Tracked by #2507; remove each entry as it moves.
	"internal/api/actionplans/authz.go hasProgramRead":           readGate2507,
	"internal/api/actionplans/authz.go hasProgramWrite":          readGate2507,
	"internal/api/audit/export.go samplesHasProgramRead":         readGate2507,
	"internal/api/checklist/handler.go Handler.ExportMarkdown":   readGate2507,
	"internal/api/checklist/handler.go Handler.LoadGeneration":   readGate2507,
	"internal/api/controldetail/authz.go hasControlRead":         readGate2507,
	"internal/api/controls/export.go controlsHasProgramRead":     readGate2507,
	"internal/api/dashboard/authz.go hasProgramRead":             readGate2507,
	"internal/api/decisions/authz.go hasProgramRead":             readGate2507,
	"internal/api/evidence/export.go evidenceHasProgramRead":     readGate2507,
	"internal/api/evidencesummary/handler.go hasControlRead":     readGate2507,
	"internal/api/exceptions/export.go exceptionsHasProgramRead": readGate2507,
	"internal/api/gapexplain/handler.go hasControlRead":          readGate2507,
	"internal/api/orgunits/authz.go hasProgramRead":              readGate2507,
	"internal/api/oscalcomponents/handler.go hasOscalRead":       readGate2507,
	"internal/api/oscalprovenance/handler.go hasOscalRead":       readGate2507,
	"internal/api/personnelsecurity/authz.go hasProgramRead":     readGate2507,
	"internal/api/policies/export.go policiesHasProgramRead":     readGate2507,
	"internal/api/risks/authz.go hasProgramRead":                 readGate2507,
}

// readGate2507 is the reason on a read gate left on OwnerRoles until #2507.
const readGate2507 = "read gate still counting a session's claim; moves to live roles in #2507"

// scanOwnerRolesReads2505 returns the functions reading x.OwnerRoles.
func scanOwnerRolesReads2505(t *testing.T) map[string]bool {
	t.Helper()
	repo := filepath.Join("..", "..")
	skip := map[string]bool{
		filepath.Join(repo, "internal", "authz"):     true,
		filepath.Join(repo, "internal", "db", "dbx"): true,
	}
	fset := token.NewFileSet()
	out := map[string]bool{}
	for _, top := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(repo, top), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if skip[path] {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(repo, path)
			if err != nil {
				return err
			}
			for _, decl := range f.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == "OwnerRoles" {
						out[filepath.ToSlash(rel)+" "+funcName2447(fn)] = true
					}
					return true
				})
			}
			return nil
		})
		if err != nil {
			t.Fatalf("scan %s: %v", top, err)
		}
	}
	return out
}

func TestOwnerRolesReads2505_OnlyAllowListed(t *testing.T) {
	t.Parallel()
	scanned := scanOwnerRolesReads2505(t)

	var keys []string
	for k := range scanned {
		keys = append(keys, k)
	}
	for k := range ownerRolesReads2505 {
		if !scanned[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		why, listed := ownerRolesReads2505[k]
		switch {
		case !listed:
			t.Errorf("%s reads OwnerRoles directly. For a JWT session that is the role claim, which outlives a revoke: "+
				"gate on authz.TenantRoleLabels / HasAnyTenantRole / AckRoleLabels instead, or list it here with why it is not a role gate", k)
		case !scanned[k]:
			t.Errorf("%s is listed but no longer reads OwnerRoles: remove it", k)
		case why == "":
			t.Errorf("%s is listed with no reason", k)
		}
	}
}
