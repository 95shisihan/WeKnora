"""Extract the upload ZIP and probe its EXE with real gRPC, without Feishu secrets."""
from pathlib import Path
import os
import socket
import subprocess
import tempfile
import zipfile
import sys
import json

import grpc
from grpc_health.v1 import health_pb2, health_pb2_grpc
import datasource_pb2 as pb
import datasource_pb2_grpc as rpc


def main():
    public = "--public" in sys.argv
    if public:
        root = Path(__file__).resolve().parents[3]
        runner = root / "plugin/test-windows.ps1"
        link = os.getenv("FEISHU_PUBLIC_TEST_URL")
        if not runner.exists() or not link:
            raise SystemExit("Public 0.2.0 requires native stdio verification: run from the WeKnora checkout with FEISHU_PUBLIC_TEST_URL set; see PUBLIC_LINKS.md")
        env = dict(os.environ, WEKNORA_WINDOWS_SANDBOX_TEST="1", WEKNORA_FEISHU_TEST_URL=link,
            WEKNORA_PYTHON_PUBLIC_BUNDLE=str(Path(__file__).resolve().parent / "dist/feishu-public-windows-x64-0.2.0.zip"))
        subprocess.run(["powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", str(runner), "-HTTPOnly"], cwd=root, env=env, check=True)
        return
    name = "feishu-public" if public else "feishu-wiki"
    version = "0.1.1" if public else "0.1.0"
    archive = Path(__file__).resolve().parent / f"dist/{name}-windows-x64-{version}.zip"
    with tempfile.TemporaryDirectory(prefix="feishu-exe-check-") as staging:
        with zipfile.ZipFile(archive) as bundle:
            assert f"bin/weknora-{name}.exe" in bundle.namelist()
            assert "plugin.yaml" in bundle.namelist()
            assert not any(Path(n).suffix in (".py", ".proto", ".ps1") for n in bundle.namelist())
            assert sum(i.file_size for i in bundle.infolist()) < 256 * 1024 * 1024
            bundle.extractall(staging)
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            address = "127.0.0.1:" + str(sock.getsockname()[1])
        env = dict(os.environ, WEKNORA_PLUGIN_ADDRESS=address)
        # A frozen executable must work without a Python installation on PATH.
        env["PATH"] = os.path.join(os.environ["SystemRoot"], "System32")
        env.pop("PYTHONPATH", None)
        process = subprocess.Popen([str(Path(staging) / f"bin/weknora-{name}.exe")],
                                   cwd=staging, env=env, creationflags=subprocess.CREATE_NO_WINDOW)
        try:
            with grpc.insecure_channel(address) as channel:
                grpc.channel_ready_future(channel).result(timeout=60)
                health = health_pb2_grpc.HealthStub(channel).Check(health_pb2.HealthCheckRequest(), timeout=5)
                assert health.status == health_pb2.HealthCheckResponse.SERVING
                stub = rpc.DatasourcePluginStub(channel)
                info = stub.GetInfo(pb.GetInfoRequest(), timeout=5)
                assert (info.id, info.version, info.connector_type, info.protocol_version) == (
                    "dev.example." + name, version, name.replace("-", "_") + "_plugin", "v1")
                try:
                    stub.Validate(pb.ConfigRequest(config_json=b"{}"), timeout=5)
                    raise AssertionError("invalid config accepted")
                except grpc.RpcError as exc:
                    assert exc.code() == grpc.StatusCode.FAILED_PRECONDITION
                print("PASS: extracted Windows EXE starts without Python on PATH; Health, identity, config validation")
                if public and os.getenv("FEISHU_PUBLIC_TEST_URL"):
                    raw = json.dumps({"settings": {"public_urls": os.environ["FEISHU_PUBLIC_TEST_URL"]}}).encode()
                    stub.Validate(pb.ConfigRequest(config_json=raw), timeout=60)
                    listing = stub.ListResources(pb.ListResourcesRequest(config_json=raw), timeout=60)
                    assert len(listing.resources) == 1
                    key = listing.resources[0].external_id
                    stub.ResolveResourceAncestors(pb.ResolveResourceAncestorsRequest(config_json=raw, resource_ids=[key]), timeout=5)
                    first = list(stub.Fetch(pb.FetchRequest(config_json=raw, resource_ids=[key], full=True), timeout=60))
                    assert len(first) == 2 and first[0].item.content
                    second = list(stub.Fetch(pb.FetchRequest(config_json=raw, resource_ids=[key], cursor_json=first[-1].final_cursor_json), timeout=60))
                    assert len(second) == 1 and second[0].final_cursor_json
                    print("PASS: live anonymous Feishu -> packaged EXE -> gRPC; initial=1, unchanged=0; content bytes=", len(first[0].item.content))
        finally:
            process.kill()
            process.wait(timeout=15)


if __name__ == "__main__":
    main()
