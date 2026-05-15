"""gRPC server exposing the OSCAL bridge to the Go platform.

Binds ``OscalBridgeService`` (see ``proto/oscal/v1/oscal.proto``). The Go
side (``internal/oscal``) dials this server, sends the platform
aggregates, and receives canonical OSCAL JSON v1.1.x bytes back.

The server is intentionally stateless: no database, no auth, no LLM. It
is meant to run as a sidecar to the platform binary on the same host (or
inside the same pod), reachable only over loopback or the pod network —
the Go side is the trust boundary.

Run:  python -m atlas_oscal_bridge.server --address 127.0.0.1:50070
"""

from __future__ import annotations

import argparse
import logging
from concurrent import futures

import grpc

from . import oscal_pb2, oscal_pb2_grpc
from .serializer import (
    SerializeError,
    round_trip_validate,
    serialize_assessment,
    serialize_poam,
    serialize_ssp,
)

_LOG = logging.getLogger("atlas_oscal_bridge")

# Default bind address — loopback only. Operators override via --address
# for a pod-network deployment.
DEFAULT_ADDRESS = "127.0.0.1:50070"


class OscalBridgeServicer(oscal_pb2_grpc.OscalBridgeServiceServicer):
    """Implements the four bridge RPCs by delegating to ``serializer``."""

    def SerializeSSP(self, request, context):  # noqa: N802 — gRPC naming
        try:
            data = serialize_ssp(request.input)
        except SerializeError as exc:
            context.abort(grpc.StatusCode.INVALID_ARGUMENT, str(exc))
        except Exception as exc:  # noqa: BLE001
            context.abort(grpc.StatusCode.INTERNAL, f"SSP serialize failed: {exc}")
        return oscal_pb2.SerializeSSPResponse(oscal_json=data)

    def SerializeAssessment(self, request, context):  # noqa: N802
        try:
            ap_json, ar_json = serialize_assessment(request.input)
        except SerializeError as exc:
            context.abort(grpc.StatusCode.INVALID_ARGUMENT, str(exc))
        except Exception as exc:  # noqa: BLE001
            context.abort(grpc.StatusCode.INTERNAL, f"assessment serialize failed: {exc}")
        return oscal_pb2.SerializeAssessmentResponse(
            assessment_plan_json=ap_json,
            assessment_results_json=ar_json,
        )

    def SerializePOAM(self, request, context):  # noqa: N802
        try:
            data = serialize_poam(request.input)
        except SerializeError as exc:
            context.abort(grpc.StatusCode.INVALID_ARGUMENT, str(exc))
        except Exception as exc:  # noqa: BLE001
            context.abort(grpc.StatusCode.INTERNAL, f"POA&M serialize failed: {exc}")
        return oscal_pb2.SerializePOAMResponse(oscal_json=data)

    def RoundTripValidate(self, request, context):  # noqa: N802
        valid, errors = round_trip_validate(request.model_type, request.oscal_json)
        return oscal_pb2.RoundTripValidateResponse(valid=valid, errors=errors)


def serve(address: str = DEFAULT_ADDRESS, max_workers: int = 8) -> grpc.Server:
    """Build, start, and return a gRPC server bound to ``address``.

    The caller owns the returned server's lifecycle (``wait_for_termination``
    or ``stop``). Tests use this to spin the server up in-process.
    """
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=max_workers))
    oscal_pb2_grpc.add_OscalBridgeServiceServicer_to_server(OscalBridgeServicer(), server)
    bound_port = server.add_insecure_port(address)
    if bound_port == 0:
        raise RuntimeError(f"oscal-bridge: failed to bind {address}")
    server.start()
    _LOG.info("oscal-bridge listening on %s", address)
    return server


def main() -> None:
    parser = argparse.ArgumentParser(description="security-atlas OSCAL bridge")
    parser.add_argument(
        "--address",
        default=DEFAULT_ADDRESS,
        help=f"bind address (default: {DEFAULT_ADDRESS})",
    )
    parser.add_argument(
        "--max-workers",
        type=int,
        default=8,
        help="gRPC thread-pool size (default: 8)",
    )
    args = parser.parse_args()
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(message)s")
    server = serve(args.address, args.max_workers)
    server.wait_for_termination()


if __name__ == "__main__":
    main()
