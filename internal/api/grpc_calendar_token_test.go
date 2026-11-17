package api_test

import (
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"

	adminv1 "github.com/mgoodric/security-atlas/gen/proto/admin/v1"
	connectorsv1 "github.com/mgoodric/security-atlas/gen/proto/connectors/v1"
	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
)

// issueKinds mints a tenantA key whose kinds are exactly kinds.
func (h *testHarness) issueKinds(t *testing.T, kinds ...string) string {
	t.Helper()
	stub, done := h.adminClient(t)
	defer done()
	issued, err := stub.Issue(bearerCtx(h.adminBearer), &adminv1.IssueRequest{
		TenantId: h.tenant,
		Kinds:    kinds,
		Ttl:      durationpb.New(time.Hour),
	})
	if err != nil {
		t.Fatalf("Issue %v: %v", kinds, err)
	}
	return issued.GetBearerToken()
}

func registerReq() *connectorsv1.RegisterRequest {
	return &connectorsv1.RegisterRequest{
		Name:              "ics-2477",
		Version:           "0.0.1",
		InstanceId:        "ics-2477-instance",
		SupportedKinds:    []string{"sast.scan_result.v1"},
		ProfilesSupported: []string{"push"},
	}
}

// #2477: an ICS feed token, a key whose kinds are exactly
// {calendar.read.v1}, is no gRPC bearer: ConnectorRegistry Register and
// List, evidence ingest and the admin service all answer Unauthenticated.
// Register and List were the real gap. In production, ingest already
// refuses the token's records by kind; this harness has no ingester (the
// slice-003 in-memory fallback), so the Push assertion is defence in
// depth.
func TestGRPC_CalendarOnlyTokenRejected(t *testing.T) {
	h := newHarness(t)
	ics := h.issueKinds(t, credstore.KindCalendarRead)
	conn := h.dial(t)
	defer func() { _ = conn.Close() }()

	_, err := connectorsv1.NewConnectorRegistryServiceClient(conn).Register(bearerCtx(ics), registerReq())
	if status.Code(err) != codes.Unauthenticated {
		t.Errorf("ConnectorRegistry.Register with ICS token: %v; want Unauthenticated", err)
	}
	_, err = connectorsv1.NewConnectorRegistryServiceClient(conn).List(bearerCtx(ics), &connectorsv1.ListRequest{})
	if status.Code(err) != codes.Unauthenticated {
		t.Errorf("ConnectorRegistry.List with ICS token: %v; want Unauthenticated", err)
	}
	_, err = evidencev1.NewEvidenceIngestServiceClient(conn).Push(bearerCtx(ics), &evidencev1.PushRequest{Record: goodRecord()})
	if status.Code(err) != codes.Unauthenticated {
		t.Errorf("EvidenceIngest.Push with ICS token: %v; want Unauthenticated", err)
	}
	_, err = adminv1.NewAdminCredentialsServiceClient(conn).List(bearerCtx(ics), &adminv1.ListRequest{TenantId: h.tenant})
	if status.Code(err) != codes.Unauthenticated {
		t.Errorf("AdminCredentials.List with ICS token: %v; want Unauthenticated", err)
	}
}

// Only an exactly-calendar key is refused: a key whose kinds include
// calendar.read.v1 among others is a general key and still authenticates.
func TestGRPC_MultiKindKeyWithCalendarAccepted(t *testing.T) {
	h := newHarness(t)
	key := h.issueKinds(t, credstore.KindCalendarRead, "sast.scan_result.v1")
	conn := h.dial(t)
	defer func() { _ = conn.Close() }()

	if _, err := connectorsv1.NewConnectorRegistryServiceClient(conn).List(bearerCtx(key), &connectorsv1.ListRequest{}); err != nil {
		t.Errorf("ConnectorRegistry.List with multi-kind key: %v; want OK", err)
	}
	if _, err := evidencev1.NewEvidenceIngestServiceClient(conn).Push(bearerCtx(key), &evidencev1.PushRequest{Record: goodRecord()}); err != nil {
		t.Errorf("EvidenceIngest.Push with multi-kind key: %v; want OK", err)
	}
}

func TestCredential_CalendarOnly(t *testing.T) {
	t.Parallel()
	cases := []struct {
		kinds []string
		want  bool
	}{
		{[]string{credstore.KindCalendarRead}, true},
		{[]string{credstore.KindCalendarRead, credstore.KindCalendarRead}, true},
		{[]string{credstore.KindCalendarRead, "sast.scan_result.v1"}, false},
		{[]string{"sast.scan_result.v1"}, false},
		{nil, false},
	}
	for _, c := range cases {
		if got := (credstore.Credential{Kinds: c.kinds}).CalendarOnly(); got != c.want {
			t.Errorf("CalendarOnly(%v) = %v; want %v", c.kinds, got, c.want)
		}
	}
}
