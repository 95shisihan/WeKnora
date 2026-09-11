"""Read anonymous Feishu SSR pages only. No login credentials or private APIs."""
import hashlib
import http.cookiejar
import json
import re
import time
from urllib.parse import urlsplit, urlunsplit
from urllib.request import Request, build_opener, HTTPRedirectHandler, HTTPCookieProcessor
from urllib.error import HTTPError, URLError
from feishu import PluginError
from host_http import HostHTTPError


def validate_url(url):
    if not isinstance(url, str):
        raise PluginError("请输入公开飞书页面链接")
    try:
        parts = urlsplit(url.strip())
        valid = (parts.scheme == "https" and not parts.username and not parts.password
                 and parts.port in (None, 443) and parts.hostname
                 and re.fullmatch(r"[a-zA-Z0-9-]+\.feishu\.cn", parts.hostname)
                 and re.fullmatch(r"/(?:article/)?(?:wiki|docx)/[A-Za-z0-9_-]+/?", parts.path))
    except ValueError:
        valid = False
    if not valid:
        raise PluginError("仅支持 https://<租户>.feishu.cn/wiki/<token> 或 /docx/<token> 公开链接；登录页面不支持")
    return urlunsplit(("https", parts.hostname, parts.path.rstrip("/"), "", ""))


class PublicRedirect(HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        # Public sharing may establish an anonymous guest cookie via the official
        # login endpoint and redirect back. Never submit credentials or a form.
        parts = urlsplit(newurl)
        guest = (parts.scheme == "https" and (parts.netloc, parts.path) in {
            ("accounts.feishu.cn", "/accounts/page/login"),
            ("login.feishu.cn", "/accounts/trap"),
        })
        if not guest:
            validate_url(newurl)
        return super().redirect_request(req, fp, code, msg, headers, newurl)


def parse_page(html):
    """Decode embedded JSON as data, never execute page JavaScript."""
    match = re.search(r"clientVars\s*:\s*Object\(", html)
    if not match:
        raise PluginError("未找到公开正文：可能需要登录、访问授权，或飞书页面格式已变化")
    try:
        payload, _ = json.JSONDecoder().raw_decode(html[match.end():].lstrip())
        data = payload["data"]
        if payload.get("code") != 0:
            raise PluginError("飞书未返回可读取的公开正文")
        if data.get("has_more") is not False or data.get("next_cursors") or data.get("skip_blocks"):
            raise PluginError("此页面正文未完整加载；当前公开导入不支持分页长文，请选择较短的单篇页面")
        blocks, root = data["block_map"], data["id"]
        if not isinstance(blocks, dict) or len(blocks) > 10000:
            raise ValueError()
        lines, seen = [], set()
        title = ""
        pending = [root]
        while pending:
            key = pending.pop()
            if key in seen or key not in blocks:
                raise PluginError("公开正文结构不完整，拒绝导入截断内容")
            seen.add(key)
            block = blocks[key]["data"]
            if block.get("hidden"):
                continue
            fragments = block.get("text", {}).get("initialAttributedTexts", {}).get("text", {})
            text = "".join(fragments[k] for k in sorted(fragments, key=int)).strip()
            if key == root:
                title = text
            elif text:
                lines.append(text)
            children = block.get("children", [])
            if not isinstance(children, list):
                raise ValueError()
            pending.extend(reversed(children))
        if not title or not lines:
            raise PluginError("页面没有可导入的文字正文（仅图片、表格或空白页面不支持）")
        return title, "\n\n".join(lines)
    except (KeyError, TypeError, ValueError, AttributeError):
        raise PluginError("飞书公开页面格式不受支持，未导入内容") from None


class PublicClient:
    def __init__(self, active=lambda: True, host_http=None, remaining=lambda: None):
        self.active = active
        self.host_http = host_http
        self.remaining = remaining

    def read(self, url):
        validate_url(url)
        if not self.active():
            raise PluginError("request cancelled")
        if self.host_http is None:
            raise PluginError("需要宿主受控 HTTP 通道；禁止回退为插件直接联网")
        try:
            remaining = self.remaining()
            if remaining is not None and remaining <= 0:
                raise PluginError("request cancelled")
            response = self.host_http.request("GET", url,
                headers={"User-Agent": ["Mozilla/5.0"]},
                timeout=min(20, remaining) if remaining is not None else 20,
                active=self.active)
            if response["statusCode"] != 200:
                raise PluginError(f"飞书公开页面返回 HTTP {response['statusCode']}")
            raw = response["body"]
            return parse_page(raw.decode("utf-8"))
        except HostHTTPError as exc:
            raise PluginError(str(exc)) from None
        except UnicodeError:
            raise PluginError("飞书页面不是有效 UTF-8 正文") from None


def links_from_config(raw):
    try:
        value = json.loads(raw)["settings"]["public_urls"]
        if not isinstance(value, str):
            raise ValueError()
        links = sorted(set(validate_url(url) for url in re.split(r"[\s,，]+", value.strip()) if url))
        if not 1 <= len(links) <= 20:
            raise PluginError("请填写 1 至 20 个公开页面链接，以逗号或换行分隔")
        return links
    except (KeyError, TypeError, ValueError):
        raise PluginError("请填写公开页面链接 public_urls") from None


def resource_id(url):
    return hashlib.sha256(url.encode()).hexdigest()


def sync_public(client, urls, selected, raw_cursor, full):
    known = {resource_id(url): url for url in urls}
    if set(selected) - known.keys():
        raise PluginError("选择的资源不在当前链接配置中")
    scope = sorted(selected or known.keys())
    previous = {}
    if raw_cursor and not full:
        try:
            cursor = json.loads(raw_cursor)["connector_cursor"]
            if cursor["version"] != 1 or cursor["scope"] != scope:
                raise PluginError("链接或选择范围已变化，请新建数据源或执行全量同步")
            previous = cursor["files"]
            if not isinstance(previous, dict) or any(not isinstance(v, str) for v in previous.values()):
                raise ValueError()
        except (KeyError, TypeError, ValueError):
            raise PluginError("公开链接同步游标无效") from None
    files, items = {}, []
    for key in scope:
        url = known[key]
        title, content = client.read(url)
        digest = hashlib.sha256((title + "\0" + content).encode()).hexdigest()
        files[key] = digest
        if previous.get(key) != digest:
            items.append(dict(external_id=key, source_resource_id=key, title=title,
                file_name=key + ".txt", content_type="text/plain", content=content.encode(), url=url,
                metadata={"sha256": digest, "source": "feishu_public_page"}))
    cursor = {"last_sync_time": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
              "connector_cursor": {"version": 1, "scope": scope, "files": files}}
    return items, json.dumps(cursor).encode()
