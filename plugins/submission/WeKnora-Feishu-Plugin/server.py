"""Datasource v1 adapter. Copy alongside feishu.py and datasource.proto."""
from concurrent import futures
import os
import threading

import grpc
from grpc_health.v1 import health, health_pb2, health_pb2_grpc
import datasource_pb2 as pb
import datasource_pb2_grpc as rpc
from feishu import Client, PluginError, ancestors, configuration, synchronize

PLUGIN_ID = "dev.example.feishu-wiki"
PLUGIN_VERSION = "0.1.0"
CONNECTOR_TYPE = "feishu_wiki_plugin"


class Datasource(rpc.DatasourcePluginServicer):
    def __init__(self, client_factory=Client):
        self.client_factory = client_factory
        self.fetch_lock = threading.Lock()

    def session(self, request, context):
        config = configuration(request.config_json)
        return config, self.client_factory(config, active=context.is_active)

    def GetInfo(self, request, context):
        return pb.GetInfoResponse(id=PLUGIN_ID, version=PLUGIN_VERSION,
                                  protocol_version="v1", connector_type=CONNECTOR_TYPE)

    def Validate(self, request, context):
        try:
            _, client = self.session(request, context)
            client.validate()
            return pb.Empty()
        except PluginError as exc:
            context.abort(grpc.StatusCode.FAILED_PRECONDITION, str(exc))

    def ListResources(self, request, context):
        try:
            _, client = self.session(request, context)
            # Check that a manually supplied parent belongs to this configured space.
            if request.parent_id:
                ancestors(client.tree(), request.parent_id)
            resources = [pb.Resource(
                external_id=node["node_token"], name=node.get("title") or node["node_token"],
                type="document" if node.get("obj_type") == "docx" else "folder",
                parent_id=request.parent_id, has_children=bool(node.get("has_child")),
                description="docx 正文" if node.get("obj_type") == "docx" else "仅遍历子节点，不导入此类型正文",
            ) for node in client.nodes(request.parent_id)]
            return pb.ListResourcesResponse(resources=resources)
        except PluginError as exc:
            context.abort(grpc.StatusCode.FAILED_PRECONDITION, str(exc))

    def ResolveResourceAncestors(self, request, context):
        try:
            _, client = self.session(request, context)
            tree = client.tree()
            return pb.ResolveResourceAncestorsResponse(ancestors=[
                pb.Ancestors(resource_id=token, external_ids=ancestors(tree, token))
                for token in request.resource_ids])
        except PluginError as exc:
            context.abort(grpc.StatusCode.FAILED_PRECONDITION, str(exc))

    def Fetch(self, request, context):
        if not self.fetch_lock.acquire(blocking=False):
            context.abort(grpc.StatusCode.RESOURCE_EXHAUSTED, "example runs one sync at a time; retry later")
        try:
            config, client = self.session(request, context)
            items, cursor = synchronize(client, config, request.resource_ids,
                                        request.cursor_json, request.full)
            for item in items:
                client.check()
                yield pb.FetchEvent(item=pb.FetchedItem(**item))
            client.check()
            yield pb.FetchEvent(final_cursor_json=cursor)
        except PluginError as exc:
            context.abort(grpc.StatusCode.FAILED_PRECONDITION, str(exc))
        finally:
            self.fetch_lock.release()


def build_server(client_factory=Client):
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=4))
    rpc.add_DatasourcePluginServicer_to_server(Datasource(client_factory), server)
    probe = health.HealthServicer()
    health_pb2_grpc.add_HealthServicer_to_server(probe, server)
    probe.set("", health_pb2.HealthCheckResponse.SERVING)
    return server


if __name__ == "__main__":
    server = build_server()
    address = os.getenv("WEKNORA_PLUGIN_ADDRESS", "127.0.0.1:50071")
    if server.add_insecure_port(address) == 0:
        raise RuntimeError("cannot bind plugin gRPC address")
    server.start()
    try:
        server.wait_for_termination()
    except KeyboardInterrupt:
        server.stop(5).wait()
