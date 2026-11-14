package artifact_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/artifact"
)

func lookupFrom(env map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := env[k]
		return v, ok
	}
}

func TestConfigFromEnv(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		env     map[string]string
		wantOK  bool
		wantErr bool
		want    artifact.EnvConfig
	}{
		{name: "unset bucket is off", env: map[string]string{artifact.EnvEndpoint: "http://minio:9000"}},
		{name: "blank bucket is off", env: map[string]string{artifact.EnvBucket: "  "}},
		{
			name:   "bucket only uses AWS virtual-hosted style",
			env:    map[string]string{artifact.EnvBucket: "b"},
			wantOK: true,
			want:   artifact.EnvConfig{Bucket: "b"},
		},
		{
			name:   "endpoint defaults path style on and public endpoint to itself",
			env:    map[string]string{artifact.EnvBucket: "b", artifact.EnvEndpoint: "http://minio:9000"},
			wantOK: true,
			want:   artifact.EnvConfig{Bucket: "b", Endpoint: "http://minio:9000", PublicEndpoint: "http://minio:9000", UsePathStyle: true},
		},
		{
			name: "explicit public endpoint and path style off",
			env: map[string]string{
				artifact.EnvBucket: "b", artifact.EnvEndpoint: "http://minio:9000",
				artifact.EnvPublicEndpoint: "https://files.example.com", artifact.EnvForcePathStyle: "false",
			},
			wantOK: true,
			want:   artifact.EnvConfig{Bucket: "b", Endpoint: "http://minio:9000", PublicEndpoint: "https://files.example.com"},
		},
		{
			name:   "path style forced on without endpoint",
			env:    map[string]string{artifact.EnvBucket: "b", artifact.EnvForcePathStyle: "true"},
			wantOK: true,
			want:   artifact.EnvConfig{Bucket: "b", UsePathStyle: true},
		},
		{
			name:    "unparseable path style is an error",
			env:     map[string]string{artifact.EnvBucket: "b", artifact.EnvForcePathStyle: "yes please"},
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok, err := artifact.ConfigFromEnv(lookupFrom(tc.env))
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if got != tc.want {
				t.Fatalf("cfg = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// fakeS3 answers HeadBucket 200 for one bucket and 404 for anything else.
func fakeS3(t *testing.T, bucket string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead && strings.Trim(r.URL.Path, "/") == bucket {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func lazyPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	// pgxpool connects lazily; Build never queries.
	pool, err := pgxpool.New(context.Background(), "postgres://nobody@127.0.0.1:1/none")
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// setStaticAWSEnv pins the SDK default chain to static test values so the
// tests never read the developer's real AWS config.
func setStaticAWSEnv(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "test-access-key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test-secret-key")
	t.Setenv("AWS_REGION", "us-east-1")
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_CONFIG_FILE", "/dev/null")
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", "/dev/null")
}

func TestBuild_ProbesBucketAndPresignsAttachmentOnPublicEndpoint(t *testing.T) {
	setStaticAWSEnv(t)
	srv := fakeS3(t, "atlas-artifacts")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	store, err := artifact.Build(ctx, lazyPool(t), artifact.EnvConfig{
		Bucket:         "atlas-artifacts",
		Endpoint:       srv.URL,
		PublicEndpoint: "http://files.example.test:9000",
		UsePathStyle:   true,
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if store.Bucket() != "atlas-artifacts" {
		t.Fatalf("Bucket() = %q", store.Bucket())
	}

	key := artifact.StorageKeyForTenant(uuid.New(), uuid.New())
	raw, err := artifact.PresignerOf(store).PresignGetObject(ctx, key, time.Minute)
	if err != nil {
		t.Fatalf("presign: %v", err)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse presigned url: %v", err)
	}
	if u.Host != "files.example.test:9000" {
		t.Fatalf("presigned host = %q, want the public endpoint", u.Host)
	}
	if u.Path != "/atlas-artifacts/"+key {
		t.Fatalf("presigned path = %q, want path-style /atlas-artifacts/%s", u.Path, key)
	}
	if got := u.Query().Get("response-content-disposition"); got != artifact.DownloadContentDisposition {
		t.Fatalf("response-content-disposition = %q, want %q", got, artifact.DownloadContentDisposition)
	}
}

func TestBuild_UnreachableBucketFails(t *testing.T) {
	setStaticAWSEnv(t)
	srv := fakeS3(t, "some-other-bucket")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := artifact.Build(ctx, lazyPool(t), artifact.EnvConfig{
		Bucket: "atlas-artifacts", Endpoint: srv.URL, PublicEndpoint: srv.URL, UsePathStyle: true,
	})
	if err == nil || !strings.Contains(err.Error(), `bucket "atlas-artifacts" unreachable`) {
		t.Fatalf("err = %v, want unreachable-bucket error", err)
	}
}
