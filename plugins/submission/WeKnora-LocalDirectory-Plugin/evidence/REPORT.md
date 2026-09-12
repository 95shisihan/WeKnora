# Windows 独立仓库插件完整同步验收

日期：2026-09-11。结论：课题第一条验收在本机 Windows 环境通过。

## 仓库与构建

- 独立仓库：`E:/Tengxun_RAG/WeKnora-LocalDirectory-Plugin`，拥有自己的 `.git`。
- 主仓：`E:/Tengxun_RAG/WeKnora`。两者是同级目录，不是主仓内的示例目录、子模块或 worktree。
- 插件源码构建提交：`af06233a2bd81739a8d0034372cca3220ebea8c8`。
- 全新独立 `.venv`，Python 3.10.21，固定公开依赖版本，3 项插件测试通过。
- `datasource.proto` 和 stdio SDK 随独立仓库交付；编译不访问主仓源码或使用本地 Go replace。
- 构建包：`dist/independent-directory-windows-x64-0.1.0.zip`，16,960,341 字节。
- ZIP SHA-256：`5f05f97b1019a9fc446480c268c154b370530625ce961ad6ffdcf793a5f15ab8`。

## 应用内验证

通过 Lite 正常认证、管理员上传和启用接口安装，插件 ID 为 `dev.example.independent-directory`，连接器类型为 `independent_directory`，独立于已有 Local Directory 插件。启用返回 healthy，元数据返回 source=external。

插件通过原生受限进程和 stdio gRPC 同宿主通信，不申请直接联网或宿主 HTTP。本文验证的是独立构建与同步链路，不将其替代逐次网络拒绝日志验收。

知识库：**独立仓库插件验收-20260911**（`db2d33a8-2dfb-41d6-957b-1ae481407443`）。

数据源：**独立仓库目录同步**（`f72ebbd2-10e4-425d-913a-eea0f94d27c6`）。

| 轮次 | 新增 | 更新 | 失败 | 解析/摘要 | 混合检索 | 纯向量检索 |
| --- | ---: | ---: | ---: | --- | --- | --- |
| 首次同步 | 2 | 0 | 0 | 两篇 completed | 4 条结果，含旧口令 | 4 条结果，含旧口令 |
| 无修改同步 | 0 | 0 | 0 | 既有结果保持 | 成功 | 成功 |
| 仅修改 Alpha | 0 | 1 | 0 | 新 Alpha completed | 4 条结果，含新口令 | 4 条结果，含新口令 |

旧口令：`晨光琥珀7319`；更新后口令：`星海松石8427`。纯向量检索请求明确设置 `disable_keywords_match: true`，未使用预先提供的查询向量；由宿主真实生成查询 Embedding 并检索。

Beta ID 始终为 `2fd613a3-7f76-421c-94ff-055b828ffce7`，哈希、更新时间及文件大小保持不变；Alpha 从 `8ad1dbb1-1f0b-4056-a3e0-8786a25b5e95` 更新为 `de3e965d-d951-45cb-8b1a-725c1144bd1a`。

## 未修改主仓的证据

验收开始和结束时，主仓 HEAD 均为 `0df9746286b10d4d08f5ffa1a4ddfb21d506d5c6`，`git status --porcelain` 均为空。宿主 EXE SHA-256 均为 `4b86963a79f7e6ecdb7c86ca0a02d3c20f00519125e08b6952503e9a8717fde4`。没有为了接入这个插件修改注册代码或重新编译、替换宿主。

安装后执行文件哈希与独立构建 ZIP 内的 EXE 一致，记录于 `acceptance.json`。宿主数据库写入完全经应用接口完成，没有插入伪造文档或索引。

## 证据文件

- [build.json](build.json)：构建源码提交、Python 版本、包大小及哈希。
- [acceptance.json](acceptance.json)：插件安装/启用、资源枚举、知识库/数据源 ID、主仓前后状态及安装文件一致性。
- [initial.json](initial.json)、[unchanged.json](unchanged.json)、[changed.json](changed.json)：实际同步日志、文档状态和检索返回正文。
- [verify_live.py](../verify_live.py)：可重复执行的应用 API 验收脚本。

## 复现与范围

仓库可在本机执行 `git clone E:/Tengxun_RAG/WeKnora-LocalDirectory-Plugin <新目录>` 后单独安装依赖和构建。当前未创建或发布 GitHub 远端仓库；“独立仓库”指真实独立本地 Git 仓库。

本次以应用 API 为主要验收证据，不声称已录制逐项页面操作视频。图形界面可查看专用知识库。Linux OCI、WFP 真实拒绝日志、第三方仅凭文档的人工盲测仍是独立验收事项。

开始时由沙箱账户创建的测试目录使宿主无法调整 ACL。最终由目录所有者仅为当前 Windows 用户授予新建测试目录的管理权限，宿主随后按 Manifest 给插件加只读 ACL；没有更改主仓安全策略。
