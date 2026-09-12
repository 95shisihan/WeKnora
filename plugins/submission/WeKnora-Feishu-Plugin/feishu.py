"""Feishu API and synchronization logic; no WeKnora imports or credentials on disk."""
import hashlib
import json
import re
import time
from urllib.error import HTTPError, URLError
from urllib.parse import urlencode
from urllib.request import Request, build_opener, HTTPRedirectHandler


class PluginError(Exception):
    pass


def identifier(value):
    if not isinstance(value, str) or not re.fullmatch(r"[A-Za-z0-9_-]+", value):
        raise PluginError("invalid space ID or node token")
    return value


def configuration(raw):
    try:
        config = json.loads(raw)
        credentials, settings = config["credentials"], config["settings"]
        app_id, secret = credentials["app_id"], credentials["app_secret"]
        if not all(isinstance(v, str) and v.strip() for v in (app_id, secret)):
            raise ValueError()
        space = identifier(settings["space_id"])
        deletion = settings.get("sync_deletions", False)
        # The current host's external-config form stores text fields as strings.
        if isinstance(deletion, str) and deletion in ("", "false", "true"):
            deletion = deletion == "true"
        if not isinstance(deletion, bool):
            raise ValueError()
        domain = settings.get("wiki_base_url", "")
        if not isinstance(domain, str):
            raise ValueError()
        if domain and not re.fullmatch(r"https://[a-zA-Z0-9-]+\.feishu\.cn", domain):
            raise PluginError("wiki_base_url must be https://<your-tenant>.feishu.cn without a trailing slash")
        return {"app_id": app_id, "app_secret": secret, "space_id": space,
                "sync_deletions": deletion, "wiki_base_url": domain}
    except (KeyError, TypeError, ValueError):
        raise PluginError("configuration requires credentials.app_id/app_secret and settings.space_id") from None


class NoRedirect(HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None  # Never forward an application secret or bearer token to a redirect.


class Client:
    BASE = "https://open.feishu.cn/open-apis"

    def __init__(self, config, active=lambda: True, sleep=time.sleep):
        self.config, self.active, self.sleep = config, active, sleep
        self.token, self.expires = "", 0
        self.opener = build_opener(NoRedirect())

    def check(self):
        if not self.active():
            raise PluginError("request cancelled")

    def _http(self, path, params=None, body=None, token=""):
        self.check()
        url = self.BASE + path + ("?" + urlencode(params) if params else "")
        headers = {"Content-Type": "application/json"}
        if token:
            headers["Authorization"] = "Bearer " + token
        request = Request(url, data=json.dumps(body).encode() if body is not None else None,
                          headers=headers)
        for attempt in range(4):
            self.check()
            retry = False
            try:
                with self.opener.open(request, timeout=20) as response:
                    raw = response.read(4 * 1024 * 1024 + 1)
                if len(raw) > 4 * 1024 * 1024:
                    raise PluginError("API response exceeds 4 MiB; split the source document")
                result = json.loads(raw)
                if not isinstance(result, dict) or "code" not in result:
                    raise PluginError("invalid Feishu response")
                if result["code"] != 99991400:
                    return result
                retry = True
            except HTTPError as exc:
                status = exc.code
                exc.close()
                if status != 429 and status < 500:
                    raise PluginError(f"Feishu HTTP {status}; check application permissions") from None
                retry = True
            except (URLError, TimeoutError, OSError):
                retry = True
            except (ValueError, UnicodeError):
                raise PluginError("invalid Feishu JSON response") from None
            if retry and attempt < 3:
                self.sleep(2 ** attempt)
        raise PluginError("Feishu unavailable or rate limited after 4 attempts")

    def authenticate(self):
        if self.token and time.monotonic() < self.expires:
            return
        result = self._http("/auth/v3/tenant_access_token/internal", body={
            "app_id": self.config["app_id"], "app_secret": self.config["app_secret"]})
        if result["code"] != 0 or not result.get("tenant_access_token"):
            raise PluginError(f"Feishu authentication failed (code={result['code']})")
        self.token = result["tenant_access_token"]
        self.expires = time.monotonic() + max(0, int(result.get("expire", 0)) - 60)

    def get(self, path, params=None):
        for attempt in range(2):
            self.authenticate()
            result = self._http(path, params=params, token=self.token)
            if result["code"] == 0:
                data = result.get("data")
                if not isinstance(data, dict):
                    raise PluginError("Feishu response is missing data")
                return data
            if result["code"] in (99991663, 99991668) and attempt == 0:
                self.token = ""
                continue
            # Do not include upstream messages: they may contain credentials or content.
            raise PluginError(f"Feishu API failed (code={result['code']}); check scopes and space membership")

    def validate(self):
        self.get("/wiki/v2/spaces/" + self.config["space_id"])

    def nodes(self, parent=""):
        params = {"page_size": 50}
        if parent:
            params["parent_node_token"] = identifier(parent)
        seen = set()
        for _ in range(1000):
            data = self.get("/wiki/v2/spaces/" + self.config["space_id"] + "/nodes", params)
            items = data.get("items", [])
            if not isinstance(items, list):
                raise PluginError("invalid node list")
            yield from items
            if not data.get("has_more"):
                return
            token = data.get("page_token")
            if not token or token in seen:
                raise PluginError("incomplete or repeated pagination; cursor not advanced")
            seen.add(token)
            params["page_token"] = token
        raise PluginError("pagination limit exceeded")

    def tree(self):
        self.validate()
        result, pending = {}, [""]
        while pending:
            parent = pending.pop()
            for node in self.nodes(parent):
                token = identifier(node["node_token"])
                if token in result:
                    raise PluginError("duplicate or cyclic node tree; retry synchronization")
                node = dict(node, parent_node_token=parent)
                result[token] = node
                if len(result) > 2000:
                    raise PluginError("example supports at most 2000 nodes per space")
                if node.get("has_child"):
                    pending.append(token)
        return result

    def content(self, node):
        token = identifier(node["obj_token"])
        data = self.get("/docx/v1/documents/" + token + "/raw_content")
        if not isinstance(data.get("content"), str):
            raise PluginError("document response is missing content")
        return data["content"]


def ancestors(tree, token):
    identifier(token)
    if token not in tree:
        raise PluginError("selected node is not visible in the configured space")
    result = []
    parent = tree[token].get("parent_node_token", "")
    while parent:
        if parent in result or parent not in tree:
            raise PluginError("incomplete node hierarchy")
        result.append(parent)
        parent = tree[parent].get("parent_node_token", "")
    return result


def synchronize(client, config, resource_ids, raw_cursor=b"", full=False):
    """Stage a complete successful scan before emitting items or deletion tombstones.

    Hash title and raw text, not edit timestamps: same-second edits cannot be lost.
    This reads every selected docx but emits only changed documents to WeKnora.
    """
    roots = sorted(set(identifier(value) for value in resource_ids))
    scope = {"app_id": config["app_id"], "space_id": config["space_id"], "roots": roots,
             "wiki_base_url": config["wiki_base_url"], "sync_deletions": config["sync_deletions"]}
    previous = {}
    if raw_cursor and not full:
        try:
            cursor = json.loads(raw_cursor)["connector_cursor"]
            if cursor["version"] != 1 or cursor["scope"] != scope:
                raise PluginError("cursor scope/version changed; create a new datasource or perform a full sync")
            previous = cursor["files"]
            if not isinstance(previous, dict) or not all(
                isinstance(k, str) and isinstance(v, str) and re.fullmatch(r"[0-9a-f]{64}", v)
                for k, v in previous.items()
            ):
                raise ValueError()
        except (KeyError, TypeError, ValueError):
            raise PluginError("invalid cursor; refusing to infer deletions") from None
    tree = client.tree()
    for root in roots:
        ancestors(tree, root)  # Missing selected roots are errors, never mass deletions.
    files, items, total = {}, [], 0
    for token, node in sorted(tree.items()):
        if roots and token not in roots and not set(ancestors(tree, token)).intersection(roots):
            continue
        if node.get("obj_type") != "docx":
            # Unsupported nodes are still traversed; don't erase previously indexed nodes.
            if token in previous:
                files[token] = previous[token]
            continue
        client.check()
        content = client.content(node).encode("utf-8")
        title = node.get("title") or token
        digest = hashlib.sha256(title.encode() + b"\0" + content).hexdigest()
        files[token] = digest
        if not full and previous.get(token) == digest:
            continue
        total += len(content)
        if len(content) > 2 * 1024 * 1024 or total > 64 * 1024 * 1024:
            raise PluginError("example staging limit exceeded (2 MiB/document, 64 MiB/changed batch)")
        items.append({"external_id": token, "title": title, "content": content,
                      "file_name": token + ".txt", "content_type": "text/plain",
                      "source_resource_id": token,
                      "url": config["wiki_base_url"] + "/wiki/" + token if config["wiki_base_url"] else "",
                      "metadata": {"space_id": config["space_id"], "obj_token": node["obj_token"],
                                   "sha256": digest}})
    missing = previous.keys() - files.keys()
    if config["sync_deletions"]:
        items.extend({"external_id": token, "is_deleted": True} for token in sorted(missing))
    else:
        files.update({token: previous[token] for token in missing})
    cursor = {"last_sync_time": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
              "connector_cursor": {"version": 1, "scope": scope, "files": files}}
    return items, json.dumps(cursor, sort_keys=True).encode()
