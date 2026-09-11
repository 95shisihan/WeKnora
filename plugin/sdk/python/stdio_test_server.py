"""Subprocess fixture for Go's real-pipe interoperability test."""
import time
import grpc
from google.protobuf.wrappers_pb2 import BytesValue, StringValue
from grpc_health.v1 import health, health_pb2, health_pb2_grpc
from host_http import HostHTTP, HostHTTPError
from stdio_grpc import StdioServer

server = StdioServer()
http = HostHTTP(server)


def echo(request, context):
    return request


def slow(request, context):
    while context.is_active():
        time.sleep(.01)
    context.abort(grpc.StatusCode.CANCELLED, "caller cancelled")


def fetch(request, context):
    try:
        response = http.request("GET", request.value, timeout=3, active=context.is_active)
        return BytesValue(value=response["body"])
    except HostHTTPError as error:
        context.abort(grpc.StatusCode.FAILED_PRECONDITION, str(error))


server.add_generic_rpc_handlers((grpc.method_handlers_generic_handler("test.Pipe", {
    "Echo": grpc.unary_unary_rpc_method_handler(echo, request_deserializer=BytesValue.FromString, response_serializer=BytesValue.SerializeToString),
    "Slow": grpc.unary_unary_rpc_method_handler(slow, request_deserializer=BytesValue.FromString, response_serializer=BytesValue.SerializeToString),
    "HTTP": grpc.unary_unary_rpc_method_handler(fetch, request_deserializer=StringValue.FromString, response_serializer=BytesValue.SerializeToString),
}),))
probe = health.HealthServicer()
probe.set("", health_pb2.HealthCheckResponse.SERVING)
health_pb2_grpc.add_HealthServicer_to_server(probe, server)
server.start()
server.wait_for_termination()
