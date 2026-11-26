package main

// #2257: every atlas-cli REST call refuses plain http:// to a non-loopback
// host before it is sent, bearer or client secret included, unless
// --allow-insecure-http is set. Each entry point below must fail with
// cmdhttp.ErrInsecureHTTP, the error the transport guard returns before it
// hands the request on, so a pass means nothing went on the wire. The
// zero-hits proof for the guard itself is in cmdhttp/guard_test.go.
//
// Not t.Parallel: these tests set the package-global `common`,
// `controlsAuth` and the cmdhttp opt-out.

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/mgoodric/security-atlas/cmd/atlas-cli/cmdhttp"
)

const insecureRemote = "http://atlas.example.test:8080"

func withInsecureRemote(t *testing.T) {
	t.Helper()
	resetControlsAuth(t)
	prevAllow := common.allowInsecureHTTP
	t.Cleanup(func() {
		common.allowInsecureHTTP = prevAllow
		cmdhttp.SetAllowInsecureHTTP(prevAllow)
	})
	common.allowInsecureHTTP = false
	cmdhttp.SetAllowInsecureHTTP(false)
	common.endpoint = insecureRemote
	common.token = "secret-bearer"
}

func wantRefused(t *testing.T, name string, err error) {
	t.Helper()
	if !errors.Is(err, cmdhttp.ErrInsecureHTTP) {
		t.Errorf("%s against %s: err = %v, want cmdhttp.ErrInsecureHTTP", name, insecureRemote, err)
	}
}

func TestRESTCommandsRefuseNonLoopbackHTTP(t *testing.T) {
	withInsecureRemote(t)
	ctx := context.Background()
	dir := t.TempDir()

	csv := filepath.Join(dir, "controls.csv")
	if err := os.WriteFile(csv, []byte("id,title\nC-1,One\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tarball := filepath.Join(dir, "bundle.tar.gz")
	if err := os.WriteFile(tarball, []byte("not really gzip"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := callControlImport(http.MethodGet, "/v1/controls/imports/b1", nil)
	wantRefused(t, "callControlImport", err)

	// controls import export (#2297) goes through callControlImport.
	err = exportImport(&bytes.Buffer{}, "b1", filepath.Join(dir, "out"))
	wantRefused(t, "controls import export", err)

	_, _, err = uploadControlImport(csv, "soc2", nil)
	wantRefused(t, "controls import", err)

	err = uploadBundleHTTP(tarball)
	wantRefused(t, "controls upload (--token)", err)

	_, err = taxonomyRequest(http.MethodGet, "/v1/taxonomy/categories", nil)
	wantRefused(t, "taxonomy", err)

	_, _, err = overlayRawRequest(http.MethodGet, "/v1/mappings/overlay", nil)
	wantRefused(t, "mappings", err)

	_, _, err = featuresDoRequest(ctx, http.MethodGet, insecureRemote+"/v1/admin/features", nil, "secret-bearer")
	wantRefused(t, "features", err)

	_, err = postResetBootstrap(ctx, insecureRemote, "secret-bearer", "", false, 0)
	wantRefused(t, "credentials reset-bootstrap", err)

	err = runDeviceCodeLogin(ctx, &bytes.Buffer{}, insecureRemote, "client-id")
	wantRefused(t, "login", err)
}

// The client_credentials path sends the client secret to the issuer, so the
// guard must refuse the token request itself, before the upload.
func TestControlsUploadOAuthRefusesNonLoopbackHTTPIssuer(t *testing.T) {
	withInsecureRemote(t)
	common.endpoint = "https://atlas.example.test"
	controlsAuth = controlsAuthState{
		useOAuth:     true,
		clientID:     "cid",
		clientSecret: "client-secret",
		issuer:       insecureRemote,
	}
	_, err := acquireControlsBearer(context.Background())
	wantRefused(t, "controls upload (OAuth issuer)", err)
}

// A loopback http:// endpoint is still allowed: that is the local-dev path.
func TestRESTCommandsAllowLoopbackHTTP(t *testing.T) {
	withInsecureRemote(t)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	common.endpoint = srv.URL

	if _, err := callControlImport(http.MethodGet, "/v1/controls/imports/b1", nil); err != nil {
		t.Fatalf("callControlImport against loopback %s: %v", srv.URL, err)
	}
	if n := hits.Load(); n != 1 {
		t.Fatalf("loopback server saw %d requests, want 1", n)
	}
}

func TestAllowInsecureHTTPFlagWarnsAndOptsOut(t *testing.T) {
	withInsecureRemote(t)

	var stderr bytes.Buffer
	common.allowInsecureHTTP = true
	applyAllowInsecureHTTP(&stderr)
	if !strings.Contains(stderr.String(), "WARNING: --allow-insecure-http") {
		t.Fatalf("stderr = %q, want the --allow-insecure-http warning", stderr.String())
	}
	// With the opt-out the guard no longer refuses; the request fails later,
	// on the unresolvable name, which is not ErrInsecureHTTP.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, _, err := featuresDoRequest(ctx, http.MethodGet, "http://atlas.invalid/v1/admin/features", nil, "t")
	if err == nil || errors.Is(err, cmdhttp.ErrInsecureHTTP) {
		t.Fatalf("with --allow-insecure-http: err = %v, want a non-guard failure", err)
	}

	stderr.Reset()
	common.allowInsecureHTTP = false
	applyAllowInsecureHTTP(&stderr)
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want no warning without the flag", stderr.String())
	}
	_, _, err = featuresDoRequest(ctx, http.MethodGet, "http://atlas.invalid/v1/admin/features", nil, "t")
	wantRefused(t, "features after clearing the opt-out", err)
}

// The flag is registered on the root command and the root's
// PersistentPreRun applies it, so every subcommand inherits it.
func TestRootRegistersAllowInsecureHTTP(t *testing.T) {
	withInsecureRemote(t)
	root := newRootCmd()
	if root.PersistentFlags().Lookup("allow-insecure-http") == nil {
		t.Fatal("--allow-insecure-http is not a persistent root flag")
	}
	if root.PersistentPreRun == nil {
		t.Fatal("root has no PersistentPreRun to apply --allow-insecure-http")
	}
	var stderr bytes.Buffer
	root.SetErr(&stderr)
	common.allowInsecureHTTP = true
	root.PersistentPreRun(root, nil)
	if !strings.Contains(stderr.String(), "WARNING") {
		t.Fatalf("PersistentPreRun stderr = %q, want the warning", stderr.String())
	}
}

// policy import posts through featuresDoRequest (also driven above), but the
// docs list it under the guard, so drive the command end to end (#2353).
func TestPolicyImportRefusesNonLoopbackHTTP(t *testing.T) {
	withInsecureRemote(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "p.md"), []byte("---\ntitle: P\n---\nbody\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ATLAS_ADMIN_TOKEN", "secret-bearer")
	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"policy", "import", dir, "--http-endpoint", insecureRemote})
	wantRefused(t, "policy import", root.Execute())
}

// noNetwork is the transport under the guard in the hook walk below: a
// request the guard lets through stops here instead of leaving the process.
type noNetwork struct{}

var errNoNetwork = errors.New("noNetwork: request passed the guard")

func (noNetwork) RoundTrip(*http.Request) (*http.Response, error) { return nil, errNoNetwork }

// guardVerdict reports what the transport guard does with a request to
// insecureRemote under the opt-out as it stands now.
func guardVerdict() error {
	req, err := http.NewRequest(http.MethodGet, insecureRemote+"/v1/x", nil)
	if err != nil {
		return err
	}
	resp, err := cmdhttp.Guard(noNetwork{}).RoundTrip(req)
	if resp != nil {
		_ = resp.Body.Close()
	}
	return err
}

// commandPaths lists the args path of every runnable command under cmd.
func commandPaths(cmd *cobra.Command, prefix []string) [][]string {
	var out [][]string
	for _, c := range cmd.Commands() {
		p := append(append([]string{}, prefix...), c.Name())
		if c.Runnable() {
			out = append(out, p)
		}
		out = append(out, commandPaths(c, p)...)
	}
	return out
}

// Every subcommand must reach the root hook that applies
// --allow-insecure-http (#2353). Cobra runs only the nearest
// PersistentPreRun(E), so a subcommand that defined its own would silently
// drop the flag. Each runnable command is executed with its body replaced
// by a probe of the transport guard: with the flag the guard lets a
// non-loopback http:// request through, without it the guard refuses. A
// command added later is walked automatically.
func TestEverySubcommandAppliesAllowInsecureHTTP(t *testing.T) {
	withInsecureRemote(t)
	paths := commandPaths(newRootCmd(), nil)
	if len(paths) < 20 {
		t.Fatalf("walked %d runnable commands, want the whole tree", len(paths))
	}
	for _, path := range paths {
		for _, withFlag := range []bool{true, false} {
			// Start from the opposite state so a pass means the hook ran.
			common.allowInsecureHTTP = !withFlag
			cmdhttp.SetAllowInsecureHTTP(!withFlag)

			root := newRootCmd()
			target, _, err := root.Find(path)
			if err != nil {
				t.Fatalf("%v: find: %v", path, err)
			}
			// Keep the hooks; replace what would need real args, flags or
			// services. ResetFlags drops local required flags; persistent
			// ones from ancestors stay, so the root flag still parses.
			var verdict error
			ran := false
			target.Args = cobra.ArbitraryArgs
			target.PreRun, target.PreRunE, target.Run = nil, nil, nil
			target.RunE = func(*cobra.Command, []string) error {
				ran = true
				verdict = guardVerdict()
				return nil
			}
			target.ResetFlags()

			args := append([]string{}, path...)
			if withFlag {
				args = append(args, "--allow-insecure-http")
			}
			root.SetArgs(args)
			root.SetOut(&bytes.Buffer{})
			root.SetErr(&bytes.Buffer{})
			if err := root.Execute(); err != nil {
				t.Errorf("%v (flag=%v): execute: %v", path, withFlag, err)
				continue
			}
			if !ran {
				t.Errorf("%v (flag=%v): command body did not run", path, withFlag)
				continue
			}
			if withFlag && !errors.Is(verdict, errNoNetwork) {
				t.Errorf("%v --allow-insecure-http: guard verdict = %v, want the request let through (does a subcommand PersistentPreRun hide the root hook?)", path, verdict)
			}
			if !withFlag && !errors.Is(verdict, cmdhttp.ErrInsecureHTTP) {
				t.Errorf("%v without the flag: guard verdict = %v, want cmdhttp.ErrInsecureHTTP", path, verdict)
			}
		}
	}
}
