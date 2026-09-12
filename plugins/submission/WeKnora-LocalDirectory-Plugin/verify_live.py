"""Reproducible local Windows Lite acceptance via normal authenticated APIs.

Credentials stay in memory. The script only creates/uses its dedicated test KB.
It never writes the host database or changes host source/configuration.
"""
import argparse
import hashlib
import json
from pathlib import Path
import subprocess
import time
import urllib.request
import urllib.error
import uuid
import zipfile

ROOT = Path(__file__).resolve().parent
EVIDENCE = ROOT / "evidence"
STATE = EVIDENCE / "local-state.json"
PLUGIN = "dev.example.independent-directory"
TYPE = "independent_directory"
ALPHA1 = "独立插件验收 Alpha。苍鹭计划的核验口令是晨光琥珀7319。该内容来自主仓之外的独立插件仓库，由宿主执行解析、分块和索引。\n"
ALPHA2 = "独立插件验收 Alpha 更新。苍鹭计划的新核验口令是星海松石8427。此次仅修改 Alpha，Beta 内容保持不变。\n"
BETA = "独立插件验收 Beta。白鲸项目的保留口令是远山青铜6528。该文件在增量同步验收期间保持不变。\n"

def write_json(path, value):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(value, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")

def git(path, *args):
    # Scope the ownership exception to this explicitly supplied local repository.
    return subprocess.check_output(["git", "-c", "safe.directory=" + path.resolve().as_posix(),
                                    "-C", str(path), *args], text=True).strip()

def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()

class API:
    def __init__(self, base):
        self.base = base.rstrip("/")
        self.token = None
        auth = self.call("POST", "/auth/auto-setup", {})
        self.token = auth["token"]

    def call(self, method, path, data=None, raw=None, content_type="application/json"):
        body = raw if raw is not None else json.dumps(data).encode() if data is not None else None
        headers = {"Content-Type": content_type}
        if self.token:
            headers["Authorization"] = "Bearer " + self.token
        request = urllib.request.Request(self.base + "/api/v1" + path, body, headers, method=method)
        try:
            with urllib.request.urlopen(request, timeout=60) as response:
                return json.load(response)
        except urllib.error.HTTPError as exc:
            raise RuntimeError(f"{method} {path}: HTTP {exc.code}: {exc.read().decode()[:1500]}") from None

    def upload(self, path, bundle):
        boundary = "weknora" + uuid.uuid4().hex
        raw = (f'--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="{bundle.name}"\r\n'
               'Content-Type: application/zip\r\n\r\n').encode() + bundle.read_bytes() + f"\r\n--{boundary}--\r\n".encode()
        return self.call("POST", path, raw=raw, content_type="multipart/form-data; boundary=" + boundary)

def data_list(result):
    if isinstance(result, list):
        return result
    data = result.get("data", result)
    if isinstance(data, list):
        return data
    for name in ("logs", "items", "knowledge", "entries"):
        if isinstance(data.get(name), list):
            return data[name]
    raise RuntimeError("Unexpected list envelope: " + str(list(result)))

def prepare(api, host):
    if STATE.exists():
        state = json.loads(STATE.read_text(encoding="utf-8"))
        if state.get("knowledge_base_id") and not state.get("prepared"):
            assert state["host_path"] == str(host.resolve())
            return complete_prepare(api, state)
        raise RuntimeError("Acceptance state already exists; resume the next phase")
    assert not ROOT.is_relative_to(host.resolve())
    assert git(ROOT, "rev-parse", "--show-toplevel").replace("\\", "/") != git(host, "rev-parse", "--show-toplevel").replace("\\", "/")
    assert not git(host, "status", "--porcelain"), "Host must be clean before acceptance"
    bundle = ROOT / "dist/independent-directory-windows-x64-0.1.0.zip"
    existing = data_list(api.call("GET", "/system/admin/plugins"))
    assert not any(p["plugin_id"] == PLUGIN for p in existing), "Plugin ID already installed"
    state = {"host_path": str(host.resolve()), "host_commit_before": git(host, "rev-parse", "HEAD"),
             "host_status_before": git(host, "status", "--porcelain"),
             "host_binary_before": sha(host / ".tools/bin/weknora-lite-dev.exe"),
             "source_repository": git(ROOT, "rev-parse", "--show-toplevel"),
             "bundle_sha256": sha(bundle), "started_at": time.strftime("%Y-%m-%dT%H:%M:%S%z")}
    write_json(STATE, state)
    installed = api.upload("/system/admin/plugins", bundle)
    state["installed"] = installed
    write_json(STATE, state)
    enabled = api.call("POST", "/system/admin/plugins/" + PLUGIN + "/enable", {})
    state["enabled"] = enabled
    write_json(STATE, state)
    types = data_list(api.call("GET", "/datasource/types"))
    connector = next(t for t in types if t["type"] == TYPE)
    assert connector["source"] == "external" and connector["plugin_id"] == PLUGIN
    state["connector"] = connector
    kbs = data_list(api.call("GET", "/knowledge-bases"))
    reference = next(k for k in kbs if k.get("name") == "飞书受控联网验证")
    config = {key: reference[key] for key in ("embedding_model_id", "summary_model_id", "chunking_config") if reference.get(key)}
    config.update(name="独立仓库插件验收-20260911", description="Windows：独立 Git 仓库构建、动态安装、完整同步、检索与增量验证", type="document")
    kb = api.call("POST", "/knowledge-bases", config)["data"]
    state["knowledge_base_id"] = kb["id"]
    write_json(STATE, state)
    complete_prepare(api, state)

def complete_prepare(api, state):
    sample = ROOT / "sample-data"
    sample.mkdir(exist_ok=True)
    (sample / "alpha.txt").write_text(ALPHA1, encoding="utf-8")
    (sample / "beta.txt").write_text(BETA, encoding="utf-8")
    ds_config = {"type": TYPE, "credentials": {}, "settings": {"root": str(sample)}, "resource_ids": []}
    api.call("POST", "/datasource/validate-credentials", {"type": TYPE, "credentials": {}, "settings": ds_config["settings"]})
    ds = api.call("POST", "/datasource", {"knowledge_base_id": state["knowledge_base_id"], "name": "独立仓库目录同步", "type": TYPE,
                  "config": ds_config, "sync_mode": "incremental", "sync_schedule": "", "conflict_strategy": "overwrite", "sync_deletions": True})
    state["data_source_id"] = ds["id"]
    write_json(STATE, state)
    resources = api.call("GET", "/datasource/" + ds["id"] + "/resources")
    state["resources"] = resources
    state["prepared"] = True
    write_json(STATE, state)
    print(json.dumps({"plugin": PLUGIN, "state": state["enabled"], "kb_id": state["knowledge_base_id"], "ds_id": ds["id"]}, ensure_ascii=False), flush=True)

def trigger(api, phase):
    state = json.loads(STATE.read_text(encoding="utf-8"))
    if phase == "changed":
        (ROOT / "sample-data/alpha.txt").write_text(ALPHA2, encoding="utf-8")
    before = data_list(api.call("GET", "/datasource/" + state["data_source_id"] + "/logs"))
    state["pending"] = {"phase": phase, "previous_log_ids": [x["id"] for x in before]}
    write_json(STATE, state)
    result = api.call("POST", "/datasource/" + state["data_source_id"] + "/sync", {})
    print(json.dumps(result, ensure_ascii=False), flush=True)

def inspect(api):
    state = json.loads(STATE.read_text(encoding="utf-8"))
    kb = state["knowledge_base_id"]
    logs = data_list(api.call("GET", "/datasource/" + state["data_source_id"] + "/logs"))
    docs = data_list(api.call("GET", "/knowledge-bases/" + kb + "/knowledge?page=1&page_size=100"))
    pending = state.get("pending", {})
    fresh = [x for x in logs if x["id"] not in pending.get("previous_log_ids", [])]
    if not fresh or fresh[0]["status"] == "running" or len(docs) != 2 or any(x["parse_status"] != "completed" for x in docs):
        print(json.dumps({"ready": False, "logs": fresh[:1], "documents": [{k:d.get(k) for k in ("id","title","parse_status","error_message")} for d in docs]}, ensure_ascii=False), flush=True)
        return False
    log = fresh[0]
    phase = pending["phase"]
    expected = {"initial": (2, 0), "unchanged": (0, 0), "changed": (0, 1)}[phase]
    assert log["status"] == "success" and (log["items_created"], log["items_updated"]) == expected, log
    assert log["items_failed"] == 0
    summaries = [{k:d.get(k) for k in ("id","title","file_name","parse_status","summary_status","file_hash","updated_at","file_size")} for d in docs]
    results = {}
    for mode, keyword_only in (("hybrid", False), ("vector_only", True)):
        query = "星海松石8427" if phase == "changed" else "晨光琥珀7319"
        result = api.call("POST", "/knowledge-bases/" + kb + "/hybrid-search", {"query_text": query,
                  "match_count": 5, "vector_threshold": 0, "keyword_threshold": 0,
                  "disable_keywords_match": keyword_only, "disable_vector_match": False})
        hits = data_list(result)
        assert hits and any(query in x.get("content", "") for x in hits), (mode, result)
        results[mode] = hits
    record = {"phase": phase, "sync_log": log, "documents": summaries, "search": results}
    if phase != "initial":
        first = json.loads((EVIDENCE / "initial.json").read_text(encoding="utf-8"))
        old = {d["title"]:d for d in first["documents"]}
        new = {d["title"]:d for d in summaries}
        assert old["beta.txt"] == new["beta.txt"], "Unchanged Beta was reprocessed"
        if phase == "unchanged":
            assert old == new, "Unchanged sync altered document records"
        else:
            assert old["alpha.txt"]["id"] != new["alpha.txt"]["id"] or old["alpha.txt"]["updated_at"] != new["alpha.txt"]["updated_at"]
    write_json(EVIDENCE / (phase + ".json"), record)
    if phase not in state.setdefault("passed_phases", []):
        state["passed_phases"].append(phase)
    write_json(STATE, state)
    print(json.dumps({"ready": True, "phase": phase, "created": log["items_created"], "updated": log["items_updated"], "documents": summaries,
                      "hybrid_hits": len(results["hybrid"]), "vector_only_hits": len(results["vector_only"])}, ensure_ascii=False), flush=True)
    return True

def finish(api):
    state = json.loads(STATE.read_text(encoding="utf-8"))
    assert set(state.get("passed_phases", [])) == {"initial", "unchanged", "changed"}
    host = Path(state["host_path"])
    assert git(host, "rev-parse", "HEAD") == state["host_commit_before"]
    assert git(host, "status", "--porcelain") == state["host_status_before"] == ""
    assert sha(host / ".tools/bin/weknora-lite-dev.exe") == state["host_binary_before"]
    state["host_commit_after"] = git(host, "rev-parse", "HEAD")
    state["host_status_after"] = ""
    state["host_binary_unchanged"] = True
    bundle = ROOT / "dist/independent-directory-windows-x64-0.1.0.zip"
    with zipfile.ZipFile(bundle) as archive:
        digest = hashlib.sha256(archive.read("bin/weknora-independent-directory.exe")).hexdigest()
    installed = host / "data/plugins" / PLUGIN / "bin/weknora-independent-directory.exe"
    assert digest == sha(installed), "Installed executable differs from independent build"
    state["installed_executable_sha256"] = digest
    state["installed_executable_matches_bundle"] = True
    state["acceptance_passed"] = True
    state["finished_at"] = time.strftime("%Y-%m-%dT%H:%M:%S%z")
    state.pop("pending", None)
    write_json(EVIDENCE / "acceptance.json", state)
    print("Independent repository acceptance passed; host source and binary unchanged.")

if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("action", choices=["prepare", "initial", "unchanged", "changed", "inspect", "wait", "finish"])
    parser.add_argument("--base", default="http://127.0.0.1:8080")
    parser.add_argument("--host", type=Path)
    args = parser.parse_args()
    api = API(args.base)
    if args.action == "prepare":
        if args.host is None:
            parser.error("prepare requires --host (used only to verify unchanged source/binary)")
        prepare(api, args.host)
    elif args.action == "inspect": inspect(api)
    elif args.action == "wait":
        deadline = time.monotonic() + 300
        while not inspect(api):
            if time.monotonic() >= deadline:
                raise TimeoutError("Parsing/indexing did not finish within 5 minutes")
            time.sleep(5)
    elif args.action == "finish": finish(api)
    else: trigger(api, args.action)
