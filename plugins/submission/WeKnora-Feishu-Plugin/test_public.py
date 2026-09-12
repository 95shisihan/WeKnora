import json
import unittest
from urllib.request import Request
from unittest.mock import patch
import grpc
import datasource_pb2 as pb
import datasource_pb2_grpc as rpc
from public_server import build_server
from public_feishu import parse_page, validate_url, links_from_config, sync_public, resource_id, PublicRedirect, PublicClient
from host_http import HostHTTPError
from feishu import PluginError

URL = "https://example.feishu.cn/wiki/abc"


def html(more=False):
    def block(text, children):
        return {"data": {"children": children, "text": {"initialAttributedTexts": {"text": {"0": text}}}}}
    return 'window.DATA={clientVars: Object(' + json.dumps({"code": 0, "data": {
        "id": "root", "has_more": more, "block_map": {"root": block("Title", ["b", "a"]),
        "a": block("second", []), "b": block("first", [])}}}) + ')}'


class FakeClient:
    def __init__(self, *args):
        self.content = "body"
    def read(self, url):
        return "title", self.content


class PublicTests(unittest.TestCase):
    def test_no_direct_network_fallback(self):
        with self.assertRaisesRegex(PluginError, "禁止回退"):
            PublicClient().read(URL)

    def test_host_response_and_permission_errors(self):
        class Host:
            def request(self, method, url, **kwargs):
                self.call = (method, url)
                return {"statusCode": 200, "body": html().encode()}
        host = Host()
        self.assertEqual(PublicClient(host_http=host).read(URL), ("Title", "first\n\nsecond"))
        self.assertEqual(host.call, ("GET", URL))
        with patch.object(host, "request", side_effect=HostHTTPError("DOMAIN_NOT_ALLOWED", "denied")):
            with self.assertRaisesRegex(PluginError, "DOMAIN_NOT_ALLOWED"):
                PublicClient(host_http=host).read(URL)

    def test_ordered_text(self):
        self.assertEqual(parse_page(html()), ("Title", "first\n\nsecond"))

    def test_login_partial_and_unknown_fail(self):
        for value in ("<title>Login</title>", html(True), "clientVars: Object({bad"):
            with self.assertRaises(PluginError):
                parse_page(value)

    def test_url_boundaries(self):
        for value in ("http://example.feishu.cn/wiki/abc", "https://127.0.0.1/wiki/abc",
                      "https://x.feishu.cn.evil.com/wiki/abc", "https://accounts.feishu.cn/login",
                      "https://a:b@example.feishu.cn/wiki/abc", "https://x.feishu.cn:8080/wiki/abc"):
            with self.assertRaises(PluginError):
                validate_url(value)
        self.assertEqual(validate_url(URL + "?from=copy"), URL)

    def test_redirect_login_denied(self):
        with self.assertRaises(PluginError):
            PublicRedirect().redirect_request(None, None, 302, "", {}, "https://accounts.feishu.cn/login")

    def test_official_anonymous_redirect_chain(self):
        request = Request(URL)
        for target in ("https://accounts.feishu.cn/accounts/page/login?app_id=2",
                       "https://login.feishu.cn/accounts/trap?redirect_uri=test",
                       URL + "?login_redirect_times=1"):
            request = PublicRedirect().redirect_request(request, None, 302, "", {}, target)
            self.assertEqual(request.full_url, target)
            self.assertIsNone(request.data)
            self.assertIsNone(request.get_header("Authorization"))

    def test_redirect_allowlist_is_exact(self):
        for target in ("http://login.feishu.cn/accounts/trap", "https://login.feishu.cn/other",
                       "https://login.feishu.cn.evil.com/accounts/trap",
                       "https://u:p@login.feishu.cn/accounts/trap",
                       "https://login.feishu.cn:8080/accounts/trap", "https://127.0.0.1/accounts/trap"):
            with self.assertRaises(PluginError):
                PublicRedirect().redirect_request(Request(URL), None, 302, "", {}, target)
        # Login endpoints are allowed only as intermediate redirects, never as input documents.
        with self.assertRaises(PluginError):
            validate_url("https://login.feishu.cn/accounts/trap")

    def test_config_no_credentials(self):
        self.assertEqual(links_from_config(json.dumps({"settings": {"public_urls": URL}})), [URL])

    def test_sync_change_and_cursor(self):
        client = FakeClient()
        first, cursor = sync_public(client, [URL], [], b"", True)
        self.assertEqual(len(first), 1)
        self.assertEqual(sync_public(client, [URL], [], cursor, False)[0], [])
        client.content = "changed"
        self.assertEqual(len(sync_public(client, [URL], [], cursor, False)[0]), 1)
        with self.assertRaises(PluginError):
            sync_public(client, [URL], ["unknown"], cursor, False)

    def test_failed_page_does_not_return_snapshot(self):
        client = FakeClient()
        def fail(url):
            raise PluginError("private")
        client.read = fail
        with self.assertRaises(PluginError):
            sync_public(client, [URL], [], b"", True)

    def test_real_grpc_public_contract(self):
        server = build_server()
        port = server.add_insecure_port("127.0.0.1:0")
        server.start()
        try:
            with patch("public_server.PublicClient", FakeClient), grpc.insecure_channel(f"127.0.0.1:{port}") as channel:
                stub = rpc.DatasourcePluginStub(channel)
                raw = json.dumps({"settings": {"public_urls": URL}}).encode()
                stub.Validate(pb.ConfigRequest(config_json=raw), timeout=5)
                resources = stub.ListResources(pb.ListResourcesRequest(config_json=raw), timeout=5)
                self.assertEqual(resources.resources[0].external_id, resource_id(URL))
                first = list(stub.Fetch(pb.FetchRequest(config_json=raw, full=True), timeout=5))
                self.assertEqual(len(first), 2)
                second = list(stub.Fetch(pb.FetchRequest(config_json=raw, cursor_json=first[-1].final_cursor_json), timeout=5))
                self.assertEqual(len(second), 1)
        finally:
            server.stop(0).wait()
