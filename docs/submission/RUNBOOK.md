# 课题一运行、测试与演示

## 环境与范围

已实测平台是 Windows x64 Lite。主仓使用 Go 1.26；部分测试依赖 CGO/GCC，前端类型检查需要 Node 与已安装的前端依赖。宿主完整文档链路还需要 DocReader、可用模型配置及相关 Lite 环境。安装和初始化参见 [主仓中文 README](../../README_CN.md)及[开发指南](../开发指南.md)。

`scripts/local-dev.ps1` 是本机开发辅助脚本，依赖仓库下 `.tools` 工具布局和 `.env.lite`，不承诺在干净克隆环境一条命令启动。不要随提交公开 `.env.lite`、模型密钥、数据库或登录令牌。独立 Python 插件构建需要 Windows x64 Python 3.10+；安装已经构建的 EXE ZIP 不要求用户安装 Python。

## 本机启动

以下命令在已经完成 `.tools` 和 `.env.lite` 配置的主仓根目录运行：

```powershell
.\scripts\local-dev.ps1 start
.\scripts\local-dev.ps1 status
Invoke-RestMethod http://127.0.0.1:8080/health
```

后端健康接口预期 `status=ok`；本机前端为 `http://127.0.0.1:5174`。服务未启动时，已有 JSON 证据仍可独立阅读。

## 自动化测试

在主仓根目录执行，测试结果以命令退出码和输出为准：

```powershell
# Manager、协议、五类示例、真实 gRPC/SQLite 增量；前端依赖存在时执行类型检查
.\plugin\test-windows.ps1

# Windows 原生隔离与管道通信；真实目录插件增量另见制品联调
.\plugin\test-native-windows.ps1

# 需具备管理员 WFP 审计权限，缺少审计时必须失败
.\plugin\test-native-windows.ps1 -RequireAudit
```

宿主 Manifest 到真实拒绝日志的独立集成测试需要先构建探针，并配置 CGO 工具链；准确命令见[禁网验收“复现”部分](../acceptance/windows-network-audit-2026-09-11.md)。仅普通禁网测试成功不能替代日志验收。

专有模型验收命令与范围见[模型验收报告](../acceptance/model-inference-2026-09-11.md)。历史通过记录见 `docs/acceptance/`；本次提交材料整理没有再次执行整套业务测试。

目录 Go 版本的原生管道与 Manager 增量测试位于 `internal/plugin/`，通过 `WEKNORA_NATIVE_DIRECTORY_EXE` 读取独立编译的 EXE。各插件制品路径配置见[外部插件目录](../../plugin/EXTERNAL-PLUGINS.md)。

## 独立插件构建与应用演示

打开主仓库的 [`plugins/submission/`](../../plugins/submission/) 目录。可直接下载并在插件管理中上传 `plugins/submission/WeKnora-LocalDirectory-Plugin/artifacts/independent-directory-windows-x64-0.1.0.zip`；如果需要从源码构建，在 `plugins/submission/WeKnora-LocalDirectory-Plugin/` 目录执行：

```powershell
python -m venv .venv
.venv/Scripts/python.exe -m pip install -r requirements.txt
.venv/Scripts/python.exe build_windows.py
```

输出 `dist/independent-directory-windows-x64-0.1.0.zip`；固定依赖信息见独立仓库 `requirements-lock.txt`。构建不读取 WeKnora 主仓源文件。

1. 在测试目录准备 `alpha.txt` 和 `beta.txt`，各写入不同核验文本。
2. 管理员进入“设置 → 插件管理”，上传独立插件 ZIP 并启用。
3. 新建测试知识库，配置可用 Embedding 模型；添加独立目录数据源，填写目录绝对路径，测试连接。
4. 首次同步：新增 2 篇，失败 0；等待解析及摘要完成，检索初始核验文本。
5. 不改文件再同步：新增 0、更新 0。
6. 仅更改 Alpha，再同步：更新 1；Beta 的 ID、哈希、更新时间保持不变，Alpha 新文本可检索。
7. 另行运行管理员禁网审计测试，展示真实 `plugin.network_denied` 事件。

独立仓库的 `verify_live.py` 提供 API 复现断言，但当前依赖本机模型配置来源知识库和固定开发布局；执行前阅读其 README。已经安装同 ID 插件的机器不要重复执行 `prepare`，该操作会拒绝重复安装。

## Linux/Docker 待验收步骤

以下命令为待执行说明，不是通过记录。需启用 AppArmor 的 Linux、Docker、`apparmor_parser` 与内核日志读取权限，在主仓根目录运行：

```bash
sudo apparmor_parser -r deploy/apparmor/weknora-plugin-no-network
sh plugin/security-probe/verify-linux.sh
```

脚本同时要求实际联网被阻止及内核日志存在匹配拒绝事件。此探针仅覆盖安全隔离，完整 Linux 插件装载与同步还需按[插件开发文档](../../plugin/README.md)的 OCI 路径另外执行并保存结果。

## 评审证据导航

- `docs/acceptance/windows-independent-plugin-2026-09-11/`：构建、安装、宿主前后状态、三轮同步及检索。
- `docs/acceptance/windows-network-audit-2026-09-11/`：管理员执行信息、原始 WFP 输出、宿主拒绝日志和普通权限失败记录。
- `docs/acceptance/model-inference-2026-09-11/`：专有模型推理验证资料。
- `plugin/ACCEPTANCE.md`：全部验收的状态与适用范围。
