# 外部插件与主仓边界

具体插件源码在独立仓库构建，管理员上传编译好的 ZIP。WeKnora 运行时只读取安装包中的 Manifest、可执行程序和运行资源，不读取插件源码。

## 独立仓库

目前为本地独立 Git 仓库，尚未配置或发布新的远端地址。正式提交前需将评审可访问的链接加入提交邮件。

| 仓库名 | 内容 | 构建入口 |
| --- | --- | --- |
| `WeKnora-LocalDirectory-Plugin` | 原有 Python 独立目录插件；`go-plugin/` 保存从主仓迁出的 Go 版本 | 根目录 `build_windows.py`；Go 版本 `go-plugin/build.ps1` |
| `WeKnora-Feishu-Plugin` | 飞书知识库与公开链接插件 | `python build_windows.py --public` 或 `python build_windows.py` |
| `WeKnora-TencentDocs-Plugin` | 腾讯文档公开文字文档插件 | `build.ps1` |
| `WeKnora-ControlledHTTP-Plugin` | 宿主受控 HTTP 演示插件 | `build.ps1` |
| `WeKnora-Feishu-NoNetwork-Plugin` | 飞书完全禁网对照插件 | `build.ps1` |
| `WeKnora-Proprietary-Model-Plugin` | HMAC/NDJSON 专有模型参考插件 | `go build -mod=mod -o bin/proprietary-model.exe .` |

独立 Go 插件随仓库保存公共协议、生成代码和 SDK 副本，使用自己的 `go.mod`，不通过 `replace` 指向主仓。SDK 更新是显式维护操作，构建时不从上级目录自动覆盖源码。

## 主仓保留内容

- `internal/plugin/`：宿主装载、生命周期、隔离与适配器。
- `plugin/proto/`、`plugin/sdk/`：公共协议与开发 SDK。
- `plugin/templates/`：最小数据源和模型启动模板，不包含飞书等厂商业务实现。
- `examples/plugins/`：四类最小协议示例，供框架回归测试使用；不随管理员上传自动安装。
- `plugin/security-probe/`：操作系统拒绝与审计的最小探针。
- `internal/plugin/internal/modeltest/` 和 `internal/plugin/testdata/model-probe/`：人工模型协议测试夹具，仅被测试或测试构建使用，不是发布给用户的插件。

## 宿主联调外部制品

宿主测试不编译具体插件源码。先在对应独立仓库构建，再将以下环境变量指向制品；未提供制品时，对应真实插件联调测试明确跳过。

| 环境变量 | 制品 |
| --- | --- |
| `WEKNORA_NATIVE_DIRECTORY_EXE` | Go 本地目录插件 EXE |
| `WEKNORA_CONTROLLED_HTTP_EXE` | 受控 HTTP 插件 EXE |
| `WEKNORA_FEISHU_DENIED_EXE` | 飞书禁网对照 EXE |
| `WEKNORA_PYTHON_PUBLIC_BUNDLE` | 飞书公开链接 ZIP |
| `WEKNORA_TENCENT_DOCS_ZIP` | 腾讯文档 ZIP |
| `WEKNORA_PROPRIETARY_MODEL_EXE` | 专有模型参考 EXE（框架 CI 使用人工测试探针） |

Windows 隔离测试还需 `WEKNORA_WINDOWS_SANDBOX_TEST=1`。在线联调需相应公开文档 URL；普通协议测试、SDK 测试及 Mock 测试不需要这些制品。

目录算法测试随 Go 插件迁出；宿主仍保留真实 gRPC/SQLite 增量集成测试，以及读取外部 EXE 的原生管道和 Manager 验收。主仓 CI 只测试框架、最小模板与测试夹具；各具体插件的构建和单测在独立仓库执行。

迁移前的原始验收记录保留其当时路径与提交 SHA，以便追溯，不应直接当作当前构建命令。最新状态见 [验收矩阵](ACCEPTANCE.md)。

2026-09-11 拆分验证：四个 Go 插件独立测试和 ZIP 构建通过；飞书 28 项测试通过；宿主协议、人工模型推理及 SQLite 增量通过。新制品的 `TestNativeDirectoryStdioIncremental`、`TestWindowsNativeManagerDirectoryRoundTrip`、`TestWindowsNativeControlledHTTP`、`TestTencentDocsNativeApproval` 均在 Windows 受限进程中通过；本轮未执行在线文档导入或 Linux OCI 验收。
