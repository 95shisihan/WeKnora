# Windows 独立仓库插件完整同步验收

2026-09-11：课题一第一条验收通过——主仓之外的独立 Git 仓库插件，无需修改主仓代码即可装载，并完成同步、解析、摘要、Embedding、索引和检索。

## 独立交付

- 主仓：`E:/Tengxun_RAG/WeKnora`。
- 独立插件仓库：`E:/Tengxun_RAG/WeKnora-LocalDirectory-Plugin`，拥有自己的 `.git`，不是子模块或 worktree；尚未发布远端。
- 插件构建源码提交：`af06233a2bd81739a8d0034372cca3220ebea8c8`。
- 插件文档及证据提交：`28b27b831ffdb9ea8fa9f1d52e3c6a3371d38532`。
- 在独立全新 Python 3.10.21 虚拟环境安装公开依赖，3 项插件测试通过，使用仓库内 Proto、stdio SDK 和 PyInstaller 构建 Windows x64 EXE；不读取主仓源码。
- 安装包：独立仓库下 `dist/independent-directory-windows-x64-0.1.0.zip`，16,960,341 字节，SHA-256 为 `5f05f97b1019a9fc446480c268c154b370530625ce961ad6ffdcf793a5f15ab8`。
- 通过正常 Lite 管理员 API 上传和启用，`dev.example.independent-directory` 返回 healthy；连接器 `independent_directory` 返回 external 来源。安装的 EXE 与 ZIP 内 EXE 哈希一致。

## 实际应用结果

知识库：[独立仓库插件验收-20260911](http://127.0.0.1:5174/platform/knowledge-bases/db2d33a8-2dfb-41d6-957b-1ae481407443)。数据源 ID：`f72ebbd2-10e4-425d-913a-eea0f94d27c6`。插件和数据源已保留，可继续使用。页面确认两篇文档及其生成摘要可见。

| 场景 | 新增 | 更新 | 失败 | 验证 |
| --- | ---: | ---: | ---: | --- |
| 首次同步 | 2 | 0 | 0 | 两篇解析/摘要 completed，混合和纯向量检索均返回含初始核验文本的结果 |
| 源端无修改 | 0 | 0 | 0 | 两篇文档 ID、哈希、更新时间、大小均不变 |
| 仅修改 Alpha | 0 | 1 | 0 | 新 Alpha 解析/摘要 completed，新核验文本可检索；Beta ID、哈希、更新时间、大小不变 |

初始核验文本 `晨光琥珀7319`，更新后 `星海松石8427`。纯向量检索禁用了关键词匹配，查询向量由宿主生成；不是只验证插件 Fetch 或数据库入队。

## 主仓未改动的证据

实际验收前后，主仓 HEAD 均为 `0df9746286b10d4d08f5ffa1a4ddfb21d506d5c6`，工作树状态均为空；宿主 EXE SHA-256 均为 `4b86963a79f7e6ecdb7c86ca0a02d3c20f00519125e08b6952503e9a8717fde4`。安装期间没有重新编译、替换或重启宿主。本报告及矩阵更新是在验收完成后才写入主仓，仅记录证据。

原始结构化记录：[构建](windows-independent-plugin-2026-09-11/build.json)、[安装与宿主前后状态](windows-independent-plugin-2026-09-11/acceptance.json)、[首次同步](windows-independent-plugin-2026-09-11/initial.json)、[无修改同步](windows-independent-plugin-2026-09-11/unchanged.json)、[单文件修改](windows-independent-plugin-2026-09-11/changed.json)。记录不包含认证令牌。独立仓库 README 和 `verify_live.py` 提供复现步骤及 API 断言。

## 范围

这是 Windows 原生受限进程 + stdio gRPC 的实际应用验收。插件声明不联网、只读指定目录；本报告不替代 WFP 逐次拒绝事件日志或第三方仅凭文档实现插件的验收。Linux OCI 验收另行进行。

首次校验遇到沙箱账户创建目录的 ACL 问题，仅对新建 sample-data 目录及两份测试文件授予当前 Windows 用户管理权限，使宿主能够按 Manifest 配置插件只读访问，没有改动主仓安全策略。
