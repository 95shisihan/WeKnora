"""Offline contract tests: real protobuf/gRPC with a deterministic Feishu HTTP stub."""
import copy
import io
import json
import unittest
from urllib.error import HTTPError
from urllib.parse import urlparse, parse_qs

import grpc
from grpc_health.v1 import health_pb2, health_pb2_grpc
import datasource_pb2 as pb
import datasource_pb2_grpc as rpc
from feishu import Client, PluginError, configuration, synchronize
from server import build_server, PLUGIN_ID

RAW = json.dumps({"credentials": {"app_id": "test-app", "app_secret": "secret-never-log"},
                  "settings": {"space_id": "123", "sync_deletions": True}}).encode()


class FakeHTTP:
    def __init__(self):
        self.nodes = {
            "a": {"node_token": "a", "obj_token": "doc-a", "obj_type": "docx",
                  "title": "A", "parent_node_token": "", "has_child": True},
            "b": {"node_token": "b", "obj_token": "doc-b", "obj_type": "docx",
                  "title": "B", "parent_node_token": "a", "has_child": False},
            "c": {"node_token": "c", "obj_token": "doc-c", "obj_type": "sheet",
                  "title": "Unsupported", "parent_node_token": "", "has_child": False},
        }
        self.text = {"doc-a": "第一篇正文", "doc-b": "第二篇正文"}
        self.calls, self.fail_doc, self.repeat, self.expired, self.limited = [], "", False, False, 0

    def open(self, request, timeout):
        parsed = urlparse(request.full_url)
        path, params = parsed.path, parse_qs(parsed.query)
        self.calls.append(path)
        if self.limited:
            self.limited -= 1
            raise HTTPError(request.full_url, 429, "limited", {}, None)
        if path.endswith("/tenant_access_token/internal"):
            assert json.loads(request.data)["app_secret"] == "secret-never-log"
            return io.BytesIO(json.dumps({"code": 0, "tenant_access_token": "test-token", "expire": 7200}).encode())
        assert request.get_header("Authorization") == "Bearer test-token"
        if self.expired:
            self.expired = False
            return io.BytesIO(b'{"code":99991668}')
        if path.endswith("/spaces/123"):
            data = {"space": {"space_id": "123"}}
        elif path.endswith("/nodes"):
            parent = params.get("parent_node_token", [""])[0]
            nodes = [copy.deepcopy(n) for n in self.nodes.values() if n["parent_node_token"] == parent]
            index = int(params.get("page_token", ["0"])[0])
            data = {"items": nodes[index:index+1], "has_more": index+1 < len(nodes), "page_token": str(index+1)}
            if self.repeat:
                data.update(has_more=True, page_token="1")
        elif path.endswith("/raw_content"):
            token = path.split("/")[-2]
            if token == self.fail_doc:
                return io.BytesIO(b'{"code":1770032,"msg":"secret-never-log"}')
            data = {"content": self.text[token]}
        else:
            raise AssertionError(path)
        return io.BytesIO(json.dumps({"code": 0, "data": data}).encode())


class SyncTests(unittest.TestCase):
    def setUp(self):
        self.http, self.config = FakeHTTP(), configuration(RAW)
        self.client = Client(self.config, sleep=lambda _: None)
        self.client.opener = self.http

    def sync(self, cursor=b"", roots=(), full=False):
        return synchronize(self.client, self.config, roots, cursor, full)

    def test_full_no_change_one_change_and_delete(self):
        items, cursor = self.sync()
        self.assertEqual([i["external_id"] for i in items], ["a", "b"])
        self.assertEqual(self.sync(cursor)[0], [])
        self.http.text["doc-b"] = "只变更 B"
        items, cursor = self.sync(cursor)
        self.assertEqual([i["external_id"] for i in items], ["b"])
        del self.http.nodes["b"]
        self.assertEqual(self.sync(cursor)[0], [{"external_id": "b", "is_deleted": True}])

    def test_title_change(self):
        _, cursor = self.sync()
        self.http.nodes["a"]["title"] = "renamed"
        self.assertEqual([i["external_id"] for i in self.sync(cursor)[0]], ["a"])

    def test_new_doc_below_unsupported_node(self):
        _, cursor = self.sync()
        self.http.nodes["c"]["has_child"] = True
        self.http.nodes["d"] = dict(self.http.nodes["b"], node_token="d", parent_node_token="c")
        self.assertEqual([i["external_id"] for i in self.sync(cursor)[0]], ["d"])

    def test_form_string_boolean(self):
        raw = json.loads(RAW)
        for value, expected in (("", False), ("false", False), ("true", True)):
            raw["settings"]["sync_deletions"] = value
            self.assertEqual(configuration(json.dumps(raw))["sync_deletions"], expected)
        raw["settings"]["sync_deletions"] = "yes"
        with self.assertRaises(PluginError):
            configuration(json.dumps(raw))

    def test_rate_limit_is_bounded(self):
        self.http.limited = 10
        with self.assertRaisesRegex(PluginError, "4 attempts"):
            self.client.validate()
        self.assertEqual(len(self.http.calls), 4)

    def test_selection_and_overlap(self):
        self.assertEqual([i["external_id"] for i in self.sync(roots=["b"])[0]], ["b"])
        self.assertEqual(len(self.sync(roots=["a", "b"])[0]), 2)

    def test_missing_selected_root_aborts(self):
        _, cursor = self.sync(roots=["b"])
        del self.http.nodes["b"]
        with self.assertRaises(PluginError):
            self.sync(cursor, roots=["b"])

    def test_no_delete_by_default(self):
        self.config["sync_deletions"] = False
        _, cursor = self.sync()
        del self.http.nodes["b"]
        items, cursor = self.sync(cursor)
        self.assertEqual(items, [])
        self.assertIn("b", json.loads(cursor)["connector_cursor"]["files"])

    def test_failures_do_not_produce_a_new_snapshot(self):
        _, cursor = self.sync()
        self.http.fail_doc = "doc-b"
        with self.assertRaisesRegex(PluginError, "1770032") as caught:
            self.sync(cursor)
        self.assertNotIn("secret-never-log", str(caught.exception))

    def test_bad_cursor_and_changed_scope(self):
        for value in (b"bad", b"[]", b'{"connector_cursor":{"files":[]}}'):
            with self.assertRaises(PluginError):
                self.sync(value)
        _, cursor = self.sync()
        with self.assertRaisesRegex(PluginError, "scope"):
            self.sync(cursor, roots=["b"])
        self.assertEqual(len(self.sync(cursor, full=True)[0]), 2)

    def test_pagination_and_broken_pagination(self):
        self.assertEqual(len(self.client.tree()), 3)
        self.http.repeat = True
        with self.assertRaises(PluginError):
            self.client.tree()

    def test_token_refresh_and_rate_retry(self):
        self.http.expired, self.http.limited = True, 2
        self.client.validate()
        auth_calls = [p for p in self.http.calls if p.endswith("/tenant_access_token/internal")]
        self.assertEqual(len(auth_calls), 4)  # two rate limits, success, forced refresh

    def test_invalid_configuration(self):
        for raw in (b"{}", b"null", b"[]"):
            with self.assertRaises(PluginError):
                configuration(raw)
        value = json.loads(RAW)
        value["settings"]["wiki_base_url"] = "https://evil.example"
        with self.assertRaises(PluginError):
            configuration(json.dumps(value))

    def test_cancellation(self):
        self.client.active = lambda: False
        with self.assertRaisesRegex(PluginError, "cancelled"):
            self.sync()


class GrpcTests(unittest.TestCase):
    def setUp(self):
        self.http = FakeHTTP()
        def factory(config, active):
            client = Client(config, active=active, sleep=lambda _: None)
            client.opener = self.http
            return client
        self.server = build_server(factory)
        port = self.server.add_insecure_port("127.0.0.1:0")
        self.server.start()
        self.channel = grpc.insecure_channel(f"127.0.0.1:{port}")
        self.stub = rpc.DatasourcePluginStub(self.channel)

    def tearDown(self):
        self.channel.close()
        self.server.stop(0).wait()

    def test_all_rpcs_and_incremental_stream(self):
        status = health_pb2_grpc.HealthStub(self.channel).Check(health_pb2.HealthCheckRequest(), timeout=5)
        self.assertEqual(status.status, health_pb2.HealthCheckResponse.SERVING)
        self.assertEqual(self.stub.GetInfo(pb.GetInfoRequest(), timeout=5).id, PLUGIN_ID)
        self.stub.Validate(pb.ConfigRequest(config_json=RAW), timeout=5)
        listing = self.stub.ListResources(pb.ListResourcesRequest(config_json=RAW), timeout=5)
        self.assertEqual([r.external_id for r in listing.resources], ["a", "c"])
        result = self.stub.ResolveResourceAncestors(pb.ResolveResourceAncestorsRequest(
            config_json=RAW, resource_ids=["b"]), timeout=5)
        self.assertEqual(list(result.ancestors[0].external_ids), ["a"])
        first = list(self.stub.Fetch(pb.FetchRequest(config_json=RAW, full=True), timeout=5))
        self.assertEqual(len(first), 3)
        self.http.text["doc-b"] += " changed"
        second = list(self.stub.Fetch(pb.FetchRequest(
            config_json=RAW, cursor_json=first[-1].final_cursor_json), timeout=5))
        self.assertEqual(len(second), 2)
        self.assertEqual(second[0].item.external_id, "b")

    def test_api_failure_emits_no_items_or_cursor(self):
        self.http.fail_doc = "doc-b"
        events = self.stub.Fetch(pb.FetchRequest(config_json=RAW, full=True), timeout=5)
        with self.assertRaises(grpc.RpcError) as caught:
            next(events)
        self.assertEqual(caught.exception.code(), grpc.StatusCode.FAILED_PRECONDITION)


if __name__ == "__main__":
    unittest.main()
