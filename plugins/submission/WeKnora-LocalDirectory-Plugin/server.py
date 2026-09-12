"""Minimal standalone WeKnora datasource plugin.

Copy this directory into a new repository, change the IDs, and replace the
filesystem-specific functions. It intentionally imports no WeKnora source.
"""

from concurrent import futures
import hashlib
import json
import mimetypes
import os
from pathlib import Path
import time

import grpc
from grpc_health.v1 import health, health_pb2, health_pb2_grpc

import datasource_pb2 as pb
import datasource_pb2_grpc as rpc


PLUGIN_ID = "dev.example.independent-directory"
PLUGIN_VERSION = os.getenv("WEKNORA_PLUGIN_VERSION", "0.1.0")
CONNECTOR_TYPE = "independent_directory"


def config_root(raw: bytes) -> Path:
    try:
        config = json.loads(raw or b"{}")
        value = config.get("settings", {}).get("root", "")
        if not isinstance(value, str) or not value or value.startswith(("\\\\", "//")):
            raise ValueError("root must be an absolute local directory")
        root = Path(value)
        if not root.is_absolute():
            raise ValueError("root must be absolute")
        root = root.resolve()
    except (ValueError, TypeError) as exc:
        raise ValueError(f"invalid config: {exc}") from exc
    if not root.is_dir():
        raise ValueError(f"directory does not exist: {root}")
    return root


def snapshot(root: Path) -> dict[str, tuple[Path, str]]:
    result: dict[str, tuple[Path, str]] = {}
    for current, directories, files in os.walk(root, followlinks=False):
        directories[:] = sorted(
            name for name in directories if not (Path(current) / name).is_symlink()
        )
        for name in sorted(files):
            path = Path(current) / name
            if path.is_symlink() or not path.is_file():
                continue
            relative = path.relative_to(root).as_posix()
            digest = hashlib.sha256(path.read_bytes()).hexdigest()
            result[relative] = (path, digest)
    return result


def previous_files(raw: bytes) -> dict[str, str]:
    if not raw:
        return {}
    try:
        cursor = json.loads(raw)
        return cursor.get("connector_cursor", {}).get("files", {}) or {}
    except (ValueError, TypeError, AttributeError) as exc:
        raise ValueError(f"invalid cursor: {exc}") from exc


def cursor_bytes(files: dict[str, str]) -> bytes:
    return json.dumps(
        {
            "last_sync_time": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
            "connector_cursor": {"files": files},
        },
        separators=(",", ":"),
        sort_keys=True,
    ).encode()


class Datasource(rpc.DatasourcePluginServicer):
    def GetInfo(self, request, context):
        return pb.GetInfoResponse(
            id=PLUGIN_ID,
            version=PLUGIN_VERSION,
            protocol_version="v1",
            connector_type=CONNECTOR_TYPE,
        )

    def Validate(self, request, context):
        try:
            config_root(request.config_json)
        except ValueError as exc:
            context.abort(grpc.StatusCode.INVALID_ARGUMENT, str(exc))
        return pb.Empty()

    def ListResources(self, request, context):
        try:
            root = config_root(request.config_json)
        except ValueError as exc:
            context.abort(grpc.StatusCode.INVALID_ARGUMENT, str(exc))
        resources = [
            pb.Resource(
                external_id=relative,
                name=path.name,
                type="file",
                modified_at_unix_milli=int(path.stat().st_mtime * 1000),
            )
            for relative, (path, _) in snapshot(root).items()
        ]
        return pb.ListResourcesResponse(resources=resources)

    def ResolveResourceAncestors(self, request, context):
        return pb.ResolveResourceAncestorsResponse(
            ancestors=[pb.Ancestors(resource_id=value) for value in request.resource_ids]
        )

    def Fetch(self, request, context):
        try:
            root = config_root(request.config_json)
            previous = {} if request.full else previous_files(request.cursor_json)
            current = snapshot(root)
        except ValueError as exc:
            context.abort(grpc.StatusCode.INVALID_ARGUMENT, str(exc))

        next_files = {relative: digest for relative, (_, digest) in current.items()}
        for relative, (path, digest) in current.items():
            if previous.get(relative) == digest:
                continue
            content_type = mimetypes.guess_type(path.name)[0] or "application/octet-stream"
            yield pb.FetchEvent(
                item=pb.FetchedItem(
                    external_id=relative,
                    title=path.name,
                    file_name=path.name,
                    content=path.read_bytes(),
                    content_type=content_type,
                    updated_at_unix_milli=int(path.stat().st_mtime * 1000),
                    metadata={"relative_path": relative, "sha256": digest},
                )
            )
        for relative in sorted(previous.keys() - current.keys()):
            yield pb.FetchEvent(
                item=pb.FetchedItem(
                    external_id=relative,
                    title=Path(relative).name,
                    file_name=Path(relative).name,
                    is_deleted=True,
                )
            )
        yield pb.FetchEvent(final_cursor_json=cursor_bytes(next_files))


def main() -> None:
    address = os.getenv("WEKNORA_PLUGIN_ADDRESS", "stdio://")
    if address.startswith("unix://"):
        socket_path = address.removeprefix("unix://")
        try:
            os.remove(socket_path)
        except FileNotFoundError:
            pass

    if address == "stdio://":
        from stdio_grpc import StdioServer
        server = StdioServer()
    else:
        server = grpc.server(futures.ThreadPoolExecutor(max_workers=4))
    rpc.add_DatasourcePluginServicer_to_server(Datasource(), server)
    health_service = health.HealthServicer()
    health_pb2_grpc.add_HealthServicer_to_server(health_service, server)
    health_service.set("", health_pb2.HealthCheckResponse.SERVING)
    if address != "stdio://" and server.add_insecure_port(address) == 0:
        raise RuntimeError(f"cannot listen on {address}")
    server.start()
    server.wait_for_termination()


if __name__ == "__main__":
    main()
