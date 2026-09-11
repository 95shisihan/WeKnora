"""Separate public-link datasource; safely coexists with the application API plugin."""
import os
from concurrent import futures
import grpc
from grpc_health.v1 import health, health_pb2, health_pb2_grpc
import datasource_pb2 as pb
import datasource_pb2_grpc as rpc
from feishu import PluginError
from public_feishu import PublicClient, links_from_config, resource_id, sync_public
from host_http import HostHTTP


class PublicDatasource(rpc.DatasourcePluginServicer):
    def __init__(self, host_http):
        self.host_http = host_http

    def client(self, context):
        return PublicClient(context.is_active, self.host_http, context.time_remaining)

    def GetInfo(self, request, context):
        return pb.GetInfoResponse(id="dev.example.feishu-public", version="0.2.0",
                                  protocol_version="v1", connector_type="feishu_public_plugin")

    def Validate(self, request, context):
        try:
            client = self.client(context)
            for url in links_from_config(request.config_json):
                client.read(url)
            return pb.Empty()
        except PluginError as exc:
            context.abort(grpc.StatusCode.FAILED_PRECONDITION, str(exc))

    def ListResources(self, request, context):
        try:
            urls = links_from_config(request.config_json)
            if request.parent_id:
                if request.parent_id not in [resource_id(url) for url in urls]:
                    raise PluginError("未知资源")
                return pb.ListResourcesResponse()
            client = self.client(context)
            return pb.ListResourcesResponse(resources=[pb.Resource(external_id=resource_id(url),
                name=client.read(url)[0], type="document", url=url) for url in urls])
        except PluginError as exc:
            context.abort(grpc.StatusCode.FAILED_PRECONDITION, str(exc))

    def ResolveResourceAncestors(self, request, context):
        try:
            known = {resource_id(url) for url in links_from_config(request.config_json)}
            if set(request.resource_ids) - known:
                raise PluginError("未知资源")
            return pb.ResolveResourceAncestorsResponse(ancestors=[
                pb.Ancestors(resource_id=key) for key in request.resource_ids])
        except PluginError as exc:
            context.abort(grpc.StatusCode.FAILED_PRECONDITION, str(exc))

    def Fetch(self, request, context):
        try:
            items, cursor = sync_public(self.client(context), links_from_config(request.config_json),
                request.resource_ids, request.cursor_json, request.full)
            for item in items:
                if not context.is_active():
                    return
                yield pb.FetchEvent(item=pb.FetchedItem(**item))
            yield pb.FetchEvent(final_cursor_json=cursor)
        except PluginError as exc:
            context.abort(grpc.StatusCode.FAILED_PRECONDITION, str(exc))


def build_server(stdio=False):
    if stdio:
        from stdio_grpc import StdioServer
        server = StdioServer()
    else:
        server = grpc.server(futures.ThreadPoolExecutor(max_workers=8))
    host_http = HostHTTP(server)
    rpc.add_DatasourcePluginServicer_to_server(PublicDatasource(host_http), server)
    probe = health.HealthServicer()
    health_pb2_grpc.add_HealthServicer_to_server(probe, server)
    probe.set("", health_pb2.HealthCheckResponse.SERVING)
    return server


if __name__ == "__main__":
    address = os.getenv("WEKNORA_PLUGIN_ADDRESS", "stdio://")
    server = build_server(stdio=address == "stdio://")
    if address != "stdio://" and server.add_insecure_port(address) == 0:
        raise RuntimeError("cannot bind public plugin address")
    server.start()
    server.wait_for_termination()
