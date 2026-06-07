package oscal

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	oscalv1 "github.com/mgoodric/security-atlas/gen/proto/oscal/v1"
)

// BridgeClient is the Go-side interface to the Python oscal-bridge gRPC
// service. It is an interface so tests can substitute a fake without a
// running Python process — the integration test wires the real client
// against a spawned bridge; the unit tests use a stub.
type BridgeClient interface {
	// SerializeSSP maps the SSP input to canonical OSCAL JSON v1.1.x.
	SerializeSSP(ctx context.Context, in *oscalv1.SspInput) ([]byte, error)
	// SerializeAssessment maps the assessment input to (AP JSON, AR JSON).
	SerializeAssessment(ctx context.Context, in *oscalv1.AssessmentInput) (apJSON, arJSON []byte, err error)
	// SerializePOAM maps the POA&M input to canonical OSCAL JSON v1.1.x.
	SerializePOAM(ctx context.Context, in *oscalv1.PoamInput) ([]byte, error)
	// RoundTripValidate parses an OSCAL document back through
	// compliance-trestle, returning whether it is structurally valid.
	RoundTripValidate(ctx context.Context, modelType string, oscalJSON []byte) (valid bool, errs []string, err error)
	// ImportCatalog deserializes + validates an inbound OSCAL catalog JSON
	// document, returning a normalized projection (or a structured
	// validation error in the response). The ingest direction of
	// invariant #8 (slice 492). The bridge never dereferences any href the
	// document references.
	ImportCatalog(ctx context.Context, oscalJSON []byte, sourceLabel string) (*oscalv1.ImportCatalogResponse, error)
	// Close releases the underlying gRPC connection.
	Close() error
}

// grpcBridge is the production BridgeClient: a thin wrapper over the
// generated gRPC stub.
type grpcBridge struct {
	conn   *grpc.ClientConn
	client oscalv1.OscalBridgeServiceClient
}

// DialBridge connects to the Python oscal-bridge service at addr (e.g.
// "127.0.0.1:50070"). The connection is insecure (no TLS): the bridge is
// a co-located sidecar reachable only over loopback or the pod network —
// the Go platform is the trust boundary. Returns ErrBridgeUnavailable on
// a dial failure.
func DialBridge(addr string) (BridgeClient, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("%w: dial %s: %v", ErrBridgeUnavailable, addr, err)
	}
	return &grpcBridge{conn: conn, client: oscalv1.NewOscalBridgeServiceClient(conn)}, nil
}

// bridgeRPCTimeout bounds a single bridge call. Serialization of a
// realistic audit period is sub-second; 30s is generous headroom for a
// cold Python process under CI contention.
const bridgeRPCTimeout = 30 * time.Second

func (b *grpcBridge) SerializeSSP(ctx context.Context, in *oscalv1.SspInput) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, bridgeRPCTimeout)
	defer cancel()
	resp, err := b.client.SerializeSSP(ctx, &oscalv1.SerializeSSPRequest{Input: in})
	if err != nil {
		return nil, err
	}
	return resp.GetOscalJson(), nil
}

func (b *grpcBridge) SerializeAssessment(ctx context.Context, in *oscalv1.AssessmentInput) ([]byte, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, bridgeRPCTimeout)
	defer cancel()
	resp, err := b.client.SerializeAssessment(ctx, &oscalv1.SerializeAssessmentRequest{Input: in})
	if err != nil {
		return nil, nil, err
	}
	return resp.GetAssessmentPlanJson(), resp.GetAssessmentResultsJson(), nil
}

func (b *grpcBridge) SerializePOAM(ctx context.Context, in *oscalv1.PoamInput) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, bridgeRPCTimeout)
	defer cancel()
	resp, err := b.client.SerializePOAM(ctx, &oscalv1.SerializePOAMRequest{Input: in})
	if err != nil {
		return nil, err
	}
	return resp.GetOscalJson(), nil
}

func (b *grpcBridge) RoundTripValidate(ctx context.Context, modelType string, oscalJSON []byte) (bool, []string, error) {
	ctx, cancel := context.WithTimeout(ctx, bridgeRPCTimeout)
	defer cancel()
	resp, err := b.client.RoundTripValidate(ctx, &oscalv1.RoundTripValidateRequest{
		ModelType: modelType,
		OscalJson: oscalJSON,
	})
	if err != nil {
		return false, nil, err
	}
	return resp.GetValid(), resp.GetErrors(), nil
}

func (b *grpcBridge) ImportCatalog(ctx context.Context, oscalJSON []byte, sourceLabel string) (*oscalv1.ImportCatalogResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, bridgeRPCTimeout)
	defer cancel()
	resp, err := b.client.ImportCatalog(ctx, &oscalv1.ImportCatalogRequest{
		OscalJson:   oscalJSON,
		SourceLabel: sourceLabel,
	})
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (b *grpcBridge) Close() error {
	if b.conn == nil {
		return nil
	}
	return b.conn.Close()
}
