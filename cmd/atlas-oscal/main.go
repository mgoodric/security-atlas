// Package main is the OSCAL bridge supervisor / health probe.
//
// The OSCAL serialization itself lives in the Python `oscal-bridge`
// service (wrapping IBM compliance-trestle) — see oscal-bridge/README.md.
// That service is started independently (`python -m
// atlas_oscal_bridge.server`) as a sidecar to the platform binary.
//
// This Go entrypoint is a thin operational tool: `atlas-oscal health`
// dials the bridge's gRPC port and runs a trivial round-trip-validate
// call to confirm the bridge is reachable and trestle is importable.
// Deployments use it as a docker/Kubernetes readiness probe for the
// bridge sidecar. Slice 030.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/oscal"
	"github.com/mgoodric/security-atlas/internal/oscal/catalogimport"
	"github.com/mgoodric/security-atlas/internal/oscal/componentimport"
	"github.com/mgoodric/security-atlas/internal/oscal/profileimport"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

const binary = "atlas-oscal"

// defaultBridgeAddr mirrors the Python server's DEFAULT_ADDRESS.
const defaultBridgeAddr = "127.0.0.1:50070"

func main() {
	args := os.Args[1:]
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		usage()
		return
	}
	switch args[0] {
	case "health":
		addr := defaultBridgeAddr
		if v := os.Getenv("OSCAL_BRIDGE_ADDR"); v != "" {
			addr = v
		}
		if len(args) > 1 {
			addr = args[1]
		}
		if err := health(addr); err != nil {
			fmt.Fprintf(os.Stderr, "%s: bridge unhealthy at %s: %v\n", binary, addr, err)
			os.Exit(1)
		}
		fmt.Printf("%s: bridge healthy at %s\n", binary, addr)
	case "import-catalog":
		if err := runImportCatalog(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "%s: import-catalog: %v\n", binary, err)
			os.Exit(1)
		}
	case "import-profile":
		if err := runImportProfile(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "%s: import-profile: %v\n", binary, err)
			os.Exit(1)
		}
	case "import-component-definition":
		if err := runImportComponentDefinition(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "%s: import-component-definition: %v\n", binary, err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "%s: unknown command %q\n", binary, args[0])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Printf(`%s — OSCAL bridge supervisor / health probe

The OSCAL serialization service is Python (oscal-bridge/); start it with:
  python -m atlas_oscal_bridge.server --address %s

Commands:
  health [addr]   dial the bridge and run a round-trip-validate probe
                  (addr defaults to $OSCAL_BRIDGE_ADDR or %s)
  import-catalog <file> [flags]
                  import an inbound OSCAL catalog JSON document (slice 492).
                  Validates it via the bridge, persists the controls as a
                  provenance-labeled imported set mapped to SCF anchors.
                  Flags:
                    --dsn         Postgres DSN (atlas_app role); env DATABASE_URL_APP
                    --tenant-id   tenant UUID to import under (required)
                    --bridge-addr oscal-bridge gRPC address (default %s)
                    --source-label declared framework label (e.g. "NIST 800-53 rev5")
                    --imported-by  operator id recorded as provenance (default atlas-oscal)
                    --role         caller role: grc_engineer (default) or admin
                    --json         emit a JSON report instead of text
  import-profile <profile-file> --catalog <file> [--catalog <file>...] [flags]
                  resolve an inbound OSCAL profile against the supplied
                  catalog(s) and persist the resolved baseline as a
                  provenance-labeled imported set mapped to SCF anchors
                  (slice 511). The profile's import/merge/modify directives
                  are resolved via compliance-trestle; an import.href that
                  names no supplied catalog is a structured error, NEVER an
                  external fetch.
                  A profile may import another SUPPLIED profile (chained
                  profile-over-profile resolution, slice 578); supply each
                  intermediate profile with --profile. The chain is bounded by
                  a maximum depth and rejected on a cycle.
                  Flags:
                    --catalog     catalog JSON the chain resolves against
                                  (repeatable; at least one required)
                    --profile     intermediate profile JSON the chain may
                                  import (repeatable; slice 578)
                    --dsn         Postgres DSN (atlas_app role); env DATABASE_URL_APP
                    --tenant-id   tenant UUID to import under (required)
                    --bridge-addr oscal-bridge gRPC address (default %s)
                    --source-label declared baseline label (e.g. "FedRAMP Moderate")
                    --imported-by  operator id recorded as provenance (default atlas-oscal)
                    --role         caller role: grc_engineer (default) or admin
                    --json         emit a JSON report instead of text
  import-component-definition <file> [flags]
                  import an inbound OSCAL component-definition JSON document
                  (slice 512) — a vendor's control-implementation CLAIMS.
                  Validates it via the bridge, persists each
                  implemented-requirement as a provenance-labeled,
                  VENDOR-ATTRIBUTED claim mapped requirement -> SCF anchor. A
                  claim is the vendor's ASSERTION; it does NOT auto-satisfy a
                  control — operator review (existing) is required to act on it.
                  Flags:
                    --dsn         Postgres DSN (atlas_app role); env DATABASE_URL_APP
                    --tenant-id   tenant UUID to import under (required)
                    --bridge-addr oscal-bridge gRPC address (default %s)
                    --source-label declared vendor / product label (e.g. "Acme Cloud")
                    --imported-by  operator id recorded as provenance (default atlas-oscal)
                    --role         caller role: grc_engineer (default) or admin
                    --json         emit a JSON report instead of text
  help            show this message
`, binary, defaultBridgeAddr, defaultBridgeAddr, defaultBridgeAddr, defaultBridgeAddr, defaultBridgeAddr)
}

// splitPositional separates the single positional <file> argument from the
// flag tokens, allowing the file to appear in any position (before or after
// flags). It assumes every flag is `--flag value` or `--flag=value`; the
// import-catalog flag set has exactly one boolean (--json), handled here.
func splitPositional(args []string) (file string, flagArgs []string, err error) {
	boolFlags := map[string]bool{"--json": true, "-json": true}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case len(a) > 0 && a[0] == '-':
			flagArgs = append(flagArgs, a)
			// Consume the value for a non-boolean, non `=`-joined flag.
			if !boolFlags[a] && !containsEquals(a) && i+1 < len(args) {
				i++
				flagArgs = append(flagArgs, args[i])
			}
		default:
			if file != "" {
				return "", nil, fmt.Errorf("exactly one <file> argument is required (saw %q and %q)", file, a)
			}
			file = a
		}
	}
	if file == "" {
		return "", nil, errors.New("exactly one <file> argument is required")
	}
	return file, flagArgs, nil
}

func containsEquals(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '=' {
			return true
		}
	}
	return false
}

// runImportCatalog implements `atlas-oscal import-catalog <file> [flags]`
// (AC-8). It reads an OSCAL catalog JSON file, validates + normalizes it via
// the bridge, persists it transactionally under the given tenant, and prints
// a text or --json report.
func runImportCatalog(args []string) error {
	fs := flag.NewFlagSet("import-catalog", flag.ContinueOnError)
	dsn := fs.String("dsn", "", "Postgres DSN (atlas_app role); env DATABASE_URL_APP")
	tenantID := fs.String("tenant-id", "", "tenant UUID to import under (required)")
	bridgeAddr := fs.String("bridge-addr", defaultBridgeAddr, "oscal-bridge gRPC address")
	sourceLabel := fs.String("source-label", "", "declared framework label")
	importedBy := fs.String("imported-by", binary, "operator id recorded as provenance")
	roleStr := fs.String("role", string(authz.RoleGRCEngineer), "caller role: grc_engineer or admin")
	asJSON := fs.Bool("json", false, "emit a JSON report instead of text")

	// Go's flag package stops at the first non-flag token, so a positional
	// <file> placed before its flags would swallow the rest. Pull the single
	// positional out (it may appear anywhere) and parse the remaining flags.
	file, flagArgs, err := splitPositional(args)
	if err != nil {
		return err
	}
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}
	if len(fs.Args()) != 0 {
		return fmt.Errorf("unexpected extra arguments: %v", fs.Args())
	}

	if *dsn == "" {
		*dsn = os.Getenv("DATABASE_URL_APP")
	}
	if *dsn == "" {
		return errors.New("--dsn or DATABASE_URL_APP is required (use the atlas_app role)")
	}
	if *tenantID == "" {
		return errors.New("--tenant-id is required")
	}

	data, err := os.ReadFile(file)
	if err != nil {
		return fmt.Errorf("read %s: %w", file, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	pool, err := pgxpool.New(ctx, *dsn)
	if err != nil {
		return fmt.Errorf("pgxpool: %w", err)
	}
	defer pool.Close()

	bridge, err := oscal.DialBridge(*bridgeAddr)
	if err != nil {
		return fmt.Errorf("connect to oscal-bridge at %s: %w", *bridgeAddr, err)
	}
	defer func() { _ = bridge.Close() }()

	tenantCtx, err := tenancy.WithTenant(ctx, *tenantID)
	if err != nil {
		return fmt.Errorf("tenancy context: %w", err)
	}

	importer := catalogimport.NewImporter(pool, bridge)
	report, err := importer.Import(tenantCtx, catalogimport.Request{
		OscalJSON:   data,
		SourceLabel: *sourceLabel,
		ImportedBy:  *importedBy,
		Role:        authz.Role(*roleStr),
	})
	if err != nil {
		switch {
		case errors.Is(err, catalogimport.ErrUnauthorizedRole):
			return fmt.Errorf("role %q may not import catalogs (requires grc_engineer or admin)", *roleStr)
		case errors.Is(err, catalogimport.ErrValidationFailed):
			return fmt.Errorf("the catalog failed OSCAL v1.1.x validation; NOTHING was persisted: %w", err)
		case errors.Is(err, catalogimport.ErrDocumentTooLarge):
			return fmt.Errorf("the catalog document is too large: %w", err)
		default:
			return err
		}
	}

	if *asJSON {
		out, _ := json.MarshalIndent(map[string]any{
			"catalog_id":    report.CatalogID.String(),
			"source_sha256": report.SourceSha256,
			"oscal_version": report.OSCALVersion,
			"catalog_title": report.CatalogTitle,
			"source_label":  report.SourceLabel,
			"control_count": report.ControlCount,
			"mapped_count":  report.MappedCount,
		}, "", "  ")
		fmt.Println(string(out))
		return nil
	}

	fmt.Printf("OSCAL catalog imported\n")
	fmt.Printf("  catalog id:     %s\n", report.CatalogID)
	fmt.Printf("  title:          %s\n", report.CatalogTitle)
	fmt.Printf("  OSCAL version:  %s\n", report.OSCALVersion)
	fmt.Printf("  source label:   %s\n", report.SourceLabel)
	fmt.Printf("  source sha256:  %s\n", report.SourceSha256)
	fmt.Printf("  controls:       %d imported (%d mapped to SCF anchors, %d need operator mapping)\n",
		report.ControlCount, report.MappedCount, report.ControlCount-report.MappedCount)
	return nil
}

// stringSlice is a repeatable string flag (one --catalog per supplied
// catalog file).
type stringSlice []string

func (s *stringSlice) String() string { return fmt.Sprintf("%v", []string(*s)) }
func (s *stringSlice) Set(v string) error {
	*s = append(*s, v)
	return nil
}

// runImportProfile implements `atlas-oscal import-profile <profile-file>
// --catalog <file>... [flags]` (AC-8). It reads an OSCAL profile JSON file
// plus the catalog file(s) it resolves against, resolves + validates the
// profile via the bridge, persists the resolved baseline transactionally
// under the given tenant, and prints a text or --json report.
func runImportProfile(args []string) error {
	fs := flag.NewFlagSet("import-profile", flag.ContinueOnError)
	var catalogs stringSlice
	fs.Var(&catalogs, "catalog", "catalog JSON the chain resolves against (repeatable)")
	var profiles stringSlice
	fs.Var(&profiles, "profile", "intermediate profile JSON the chain may import (repeatable; slice 578)")
	dsn := fs.String("dsn", "", "Postgres DSN (atlas_app role); env DATABASE_URL_APP")
	tenantID := fs.String("tenant-id", "", "tenant UUID to import under (required)")
	bridgeAddr := fs.String("bridge-addr", defaultBridgeAddr, "oscal-bridge gRPC address")
	sourceLabel := fs.String("source-label", "", "declared baseline label")
	importedBy := fs.String("imported-by", binary, "operator id recorded as provenance")
	roleStr := fs.String("role", string(authz.RoleGRCEngineer), "caller role: grc_engineer or admin")
	asJSON := fs.Bool("json", false, "emit a JSON report instead of text")

	// Pull the single positional <profile-file> out (it may appear anywhere)
	// and parse the remaining flags. Unlike import-catalog, import-profile
	// has a repeatable value flag (--catalog), so splitPositional's
	// "consume the value after a non-boolean flag" rule covers it.
	file, flagArgs, err := splitPositional(args)
	if err != nil {
		return err
	}
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}
	if len(fs.Args()) != 0 {
		return fmt.Errorf("unexpected extra arguments: %v", fs.Args())
	}

	if *dsn == "" {
		*dsn = os.Getenv("DATABASE_URL_APP")
	}
	if *dsn == "" {
		return errors.New("--dsn or DATABASE_URL_APP is required (use the atlas_app role)")
	}
	if *tenantID == "" {
		return errors.New("--tenant-id is required")
	}
	if len(catalogs) == 0 {
		return errors.New("at least one --catalog <file> is required (the catalog the profile resolves against)")
	}

	profileData, err := os.ReadFile(file)
	if err != nil {
		return fmt.Errorf("read %s: %w", file, err)
	}
	catalogData := make([][]byte, 0, len(catalogs))
	for _, cf := range catalogs {
		data, rerr := os.ReadFile(cf)
		if rerr != nil {
			return fmt.Errorf("read catalog %s: %w", cf, rerr)
		}
		catalogData = append(catalogData, data)
	}
	profileData2 := make([][]byte, 0, len(profiles))
	for _, pf := range profiles {
		data, rerr := os.ReadFile(pf)
		if rerr != nil {
			return fmt.Errorf("read intermediate profile %s: %w", pf, rerr)
		}
		profileData2 = append(profileData2, data)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	pool, err := pgxpool.New(ctx, *dsn)
	if err != nil {
		return fmt.Errorf("pgxpool: %w", err)
	}
	defer pool.Close()

	bridge, err := oscal.DialBridge(*bridgeAddr)
	if err != nil {
		return fmt.Errorf("connect to oscal-bridge at %s: %w", *bridgeAddr, err)
	}
	defer func() { _ = bridge.Close() }()

	tenantCtx, err := tenancy.WithTenant(ctx, *tenantID)
	if err != nil {
		return fmt.Errorf("tenancy context: %w", err)
	}

	importer := profileimport.NewImporter(pool, bridge)
	report, err := importer.Import(tenantCtx, profileimport.Request{
		ProfileJSON: profileData,
		Catalogs:    catalogData,
		Profiles:    profileData2,
		SourceLabel: *sourceLabel,
		ImportedBy:  *importedBy,
		Role:        authz.Role(*roleStr),
	})
	if err != nil {
		switch {
		case errors.Is(err, profileimport.ErrUnauthorizedRole):
			return fmt.Errorf("role %q may not import profiles (requires grc_engineer or admin)", *roleStr)
		case errors.Is(err, profileimport.ErrResolutionFailed):
			return fmt.Errorf("the profile failed to resolve; NOTHING was persisted: %w", err)
		case errors.Is(err, profileimport.ErrDocumentTooLarge):
			return fmt.Errorf("a document is too large: %w", err)
		case errors.Is(err, profileimport.ErrNoCatalogs):
			return fmt.Errorf("supply at least one --catalog: %w", err)
		default:
			return err
		}
	}

	if *asJSON {
		out, _ := json.MarshalIndent(map[string]any{
			"profile_id":    report.ProfileID.String(),
			"source_sha256": report.SourceSha256,
			"oscal_version": report.OSCALVersion,
			"profile_title": report.ProfileTitle,
			"source_label":  report.SourceLabel,
			"control_count": report.ControlCount,
			"mapped_count":  report.MappedCount,
		}, "", "  ")
		fmt.Println(string(out))
		return nil
	}

	fmt.Printf("OSCAL profile resolved + imported\n")
	fmt.Printf("  profile id:     %s\n", report.ProfileID)
	fmt.Printf("  profile title:  %s\n", report.ProfileTitle)
	fmt.Printf("  OSCAL version:  %s\n", report.OSCALVersion)
	fmt.Printf("  source label:   %s\n", report.SourceLabel)
	fmt.Printf("  source sha256:  %s\n", report.SourceSha256)
	fmt.Printf("  controls:       %d resolved (%d mapped to SCF anchors, %d need operator mapping)\n",
		report.ControlCount, report.MappedCount, report.ControlCount-report.MappedCount)
	return nil
}

// runImportComponentDefinition implements `atlas-oscal
// import-component-definition <file> [flags]` (AC-9). It reads an OSCAL
// component-definition JSON file, validates + normalizes it via the bridge,
// persists the vendor claims transactionally under the given tenant, and
// prints a text or --json report.
func runImportComponentDefinition(args []string) error {
	fs := flag.NewFlagSet("import-component-definition", flag.ContinueOnError)
	dsn := fs.String("dsn", "", "Postgres DSN (atlas_app role); env DATABASE_URL_APP")
	tenantID := fs.String("tenant-id", "", "tenant UUID to import under (required)")
	bridgeAddr := fs.String("bridge-addr", defaultBridgeAddr, "oscal-bridge gRPC address")
	sourceLabel := fs.String("source-label", "", "declared vendor / product label")
	importedBy := fs.String("imported-by", binary, "operator id recorded as provenance")
	roleStr := fs.String("role", string(authz.RoleGRCEngineer), "caller role: grc_engineer or admin")
	asJSON := fs.Bool("json", false, "emit a JSON report instead of text")

	file, flagArgs, err := splitPositional(args)
	if err != nil {
		return err
	}
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}
	if len(fs.Args()) != 0 {
		return fmt.Errorf("unexpected extra arguments: %v", fs.Args())
	}

	if *dsn == "" {
		*dsn = os.Getenv("DATABASE_URL_APP")
	}
	if *dsn == "" {
		return errors.New("--dsn or DATABASE_URL_APP is required (use the atlas_app role)")
	}
	if *tenantID == "" {
		return errors.New("--tenant-id is required")
	}

	data, err := os.ReadFile(file)
	if err != nil {
		return fmt.Errorf("read %s: %w", file, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	pool, err := pgxpool.New(ctx, *dsn)
	if err != nil {
		return fmt.Errorf("pgxpool: %w", err)
	}
	defer pool.Close()

	bridge, err := oscal.DialBridge(*bridgeAddr)
	if err != nil {
		return fmt.Errorf("connect to oscal-bridge at %s: %w", *bridgeAddr, err)
	}
	defer func() { _ = bridge.Close() }()

	tenantCtx, err := tenancy.WithTenant(ctx, *tenantID)
	if err != nil {
		return fmt.Errorf("tenancy context: %w", err)
	}

	importer := componentimport.NewImporter(pool, bridge)
	report, err := importer.Import(tenantCtx, componentimport.Request{
		OscalJSON:   data,
		SourceLabel: *sourceLabel,
		ImportedBy:  *importedBy,
		Role:        authz.Role(*roleStr),
	})
	if err != nil {
		switch {
		case errors.Is(err, componentimport.ErrUnauthorizedRole):
			return fmt.Errorf("role %q may not import component-definitions (requires grc_engineer or admin)", *roleStr)
		case errors.Is(err, componentimport.ErrValidationFailed):
			return fmt.Errorf("the component-definition failed OSCAL v1.1.x validation; NOTHING was persisted: %w", err)
		case errors.Is(err, componentimport.ErrDocumentTooLarge):
			return fmt.Errorf("the component-definition document is too large: %w", err)
		default:
			return err
		}
	}

	if *asJSON {
		out, _ := json.MarshalIndent(map[string]any{
			"import_id":       report.ImportID.String(),
			"source_sha256":   report.SourceSha256,
			"oscal_version":   report.OSCALVersion,
			"title":           report.Title,
			"source_label":    report.SourceLabel,
			"component_count": report.ComponentCount,
			"claim_count":     report.ClaimCount,
			"mapped_count":    report.MappedCount,
		}, "", "  ")
		fmt.Println(string(out))
		return nil
	}

	fmt.Printf("OSCAL component-definition imported (vendor claims)\n")
	fmt.Printf("  import id:      %s\n", report.ImportID)
	fmt.Printf("  title:          %s\n", report.Title)
	fmt.Printf("  OSCAL version:  %s\n", report.OSCALVersion)
	fmt.Printf("  vendor label:   %s\n", report.SourceLabel)
	fmt.Printf("  source sha256:  %s\n", report.SourceSha256)
	fmt.Printf("  components:     %d\n", report.ComponentCount)
	fmt.Printf("  vendor claims:  %d imported (%d mapped to SCF anchors, %d need operator mapping)\n",
		report.ClaimCount, report.MappedCount, report.ClaimCount-report.MappedCount)
	fmt.Printf("  NOTE: these are VENDOR ASSERTIONS, not platform-verified evidence;\n")
	fmt.Printf("        they do NOT mark any control satisfied (operator review required).\n")
	return nil
}

// health dials the bridge and runs a RoundTripValidate against an
// intentionally-malformed document. A reachable bridge with a working
// trestle import returns valid=false (the document IS invalid) with no
// transport error — that is the success condition. A transport error
// means the bridge is down or trestle failed to import.
func health(addr string) error {
	bridge, err := oscal.DialBridge(addr)
	if err != nil {
		return err
	}
	defer func() { _ = bridge.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// A garbage document: a healthy bridge answers valid=false cleanly.
	valid, _, err := bridge.RoundTripValidate(ctx, "system-security-plan", []byte("{not-oscal"))
	if err != nil {
		return err
	}
	if valid {
		return fmt.Errorf("bridge reported a garbage document as valid — trestle wiring is broken")
	}
	return nil
}
