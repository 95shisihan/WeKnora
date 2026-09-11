"""Read-only live Feishu -> real gRPC smoke test. Credentials stay in environment."""
import argparse
import json
import os

import grpc
import datasource_pb2 as pb
import datasource_pb2_grpc as rpc
from server import build_server


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--pause", action="store_true", help="pause between rounds to edit one docx")
    args = parser.parse_args()
    raw = json.dumps({
        "credentials": {"app_id": os.environ["FEISHU_APP_ID"],
                        "app_secret": os.environ["FEISHU_APP_SECRET"]},
        "settings": {"space_id": os.environ["FEISHU_SPACE_ID"]},
    }).encode()
    roots = [s.strip() for s in os.getenv("FEISHU_NODE_TOKENS", "").split(",") if s.strip()]
    server = build_server()
    port = server.add_insecure_port("127.0.0.1:0")
    server.start()
    try:
        with grpc.insecure_channel(f"127.0.0.1:{port}") as channel:
            stub = rpc.DatasourcePluginStub(channel)
            stub.Validate(pb.ConfigRequest(config_json=raw), timeout=60)
            cursor = b""
            for round_number in (1, 2):
                if round_number == 2 and args.pause:
                    input("请只修改一个选中范围内的 docx，保存并等待飞书可读后按 Enter：")
                count, final = 0, None
                for event in stub.Fetch(pb.FetchRequest(config_json=raw, resource_ids=roots,
                                                        cursor_json=cursor, full=round_number == 1), timeout=600):
                    if event.WhichOneof("payload") == "item":
                        count += 1
                    elif event.WhichOneof("payload") == "final_cursor_json":
                        final = event.final_cursor_json
                if final is None:
                    raise RuntimeError("missing final cursor")
                cursor = final
                print(f"round={round_number} emitted_documents={count} final_cursor=received")
    finally:
        server.stop(0).wait()


if __name__ == "__main__":
    main()
