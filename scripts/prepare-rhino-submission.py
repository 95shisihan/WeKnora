#!/usr/bin/env python3
"""Generate submission metadata from an existing final tag; never publish."""
import argparse
import json
from pathlib import Path
import re
import subprocess
import sys


def git(repo, *args):
    result = subprocess.run(
        ["git", "-c", f"safe.directory={repo.as_posix()}", "-C", str(repo), *args],
        capture_output=True, text=True, encoding="utf-8", errors="replace",
    )
    if result.returncode:
        raise ValueError("Git command failed: " + " ".join(args))
    return result.stdout.strip()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--name", required=True)
    parser.add_argument("--github-id", required=True)
    parser.add_argument("--tag", required=True)
    parser.add_argument("--topic-id", default="1")
    parser.add_argument("--result-url")
    parser.add_argument("--repo", type=Path, default=Path(__file__).resolve().parents[1])
    args = parser.parse_args()
    for value in (args.name, args.github_id, args.topic_id, args.tag):
        if not value.strip() or any(c in value for c in "\r\n"):
            raise ValueError("Identity, topic and tag must be nonempty single-line values.")
    if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9-]*", args.github_id):
        raise ValueError("Invalid GitHub ID.")
    repo = args.repo.resolve()
    git(repo, "check-ref-format", "refs/tags/" + args.tag)
    sha = git(repo, "rev-parse", "--verify", "refs/tags/" + args.tag + "^{commit}")
    if not re.fullmatch(r"[0-9a-f]{40}", sha):
        raise ValueError("Expected a complete 40-character commit SHA.")
    if sha != git(repo, "rev-parse", "HEAD"):
        raise ValueError("HEAD must equal the final tag before the metadata commit; do not move the tag.")
    branch = git(repo, "branch", "--show-current")
    if not branch:
        raise ValueError("Detached HEAD: switch to the submission branch first.")
    if git(repo, "status", "--porcelain"):
        raise ValueError("Commit/review pending changes before generating final metadata.")
    remote = git(repo, "config", "--get", "remote.origin.url")
    match = re.fullmatch(r"(?:git@github\.com:|https://github\.com/)([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)", remote)
    if not match:
        raise ValueError("Use a credential-free GitHub SSH/HTTPS origin URL.")
    slug = match.group(1).removesuffix(".git")
    url = args.result_url or "https://github.com/" + slug
    if not re.fullmatch(r"https://[^\s@]+", url):
        raise ValueError("Result URL must be credential-free HTTPS.")
    q = lambda value: json.dumps(value, ensure_ascii=False)
    yaml = f'''version: 1
student:
  name: {q(args.name)}
  github_id: {q(args.github_id)}
topic:
  id: {q(args.topic_id)}
  title: "扩展能力插件化框架"
result:
  type: code
  url: {q(url)}
repository:
  url: {q(remote)}
  branch: {q(branch)}
  commit: {q(sha)}
  tag: {q(args.tag)}
'''
    output = repo / "submission.yaml"
    if output.exists():
        raise ValueError("submission.yaml already exists; review it manually, no automatic overwrite.")
    mail_dir = repo.parent / "最终提交材料"
    template = mail_dir / "邮件草稿.md"
    final_mail = mail_dir / "邮件_最终草稿.md"
    mail = None
    if template.exists():
        if final_mail.exists():
            raise ValueError("Final email draft already exists; review it manually.")
        mail = template.read_text(encoding="utf-8")
        mail = mail.replace("待填写姓名", args.name).replace("待确认GitHub ID", args.github_id)
        mail = mail.replace("姓名：待填写", "姓名：" + args.name)
        mail = mail.replace("GitHub ID：待确认", "GitHub ID：" + args.github_id)
        mail = mail.replace("【课题一】", "【" + args.topic_id + "】")
        mail = mail.replace("最终 Tag 待创建；完整 Commit SHA 待冻结后生成", f"Tag：{args.tag}；Commit SHA：{sha}")
        mail = mail.replace("https://github.com/95shisihan/WeKnora（发送前确认可访问且已推送最终版本）", url)
        mail = mail.replace("submission.yaml（需在最终 Tag 创建后生成正式文件）", "submission.yaml")
        mail = mail.replace("# 邮件草稿：待补身份、最终版本及独立仓库链接", "# 邮件草稿：身份与版本已填，发送前补独立仓库链接并核对最新验收状态")
        mail += f"\n版本固定的阅读入口：https://github.com/{slug}/tree/{sha}/docs/submission\n"
    output.write_text(yaml, encoding="utf-8")
    if mail is not None:
        final_mail.write_text(mail, encoding="utf-8")
    print(f"Generated {output}; tag commit: {sha}")
    print("Review, commit and push submission.yaml without moving the tag. No email was sent.")


if __name__ == "__main__":
    try:
        main()
    except (ValueError, OSError) as exc:
        print(f"ERROR: {exc}", file=sys.stderr)
        sys.exit(1)
