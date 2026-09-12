# 课题一提交说明：扩展能力插件化框架

本成果为 2026 年腾讯犀牛鸟开源人才培养计划课题一的代码类提交。材料整理于 2026-09-11；最终代码版本以仓库根目录 `submission.yaml` 的 Tag 和完整 Commit SHA 为准。当前整理基线为 `cb7a5b9f28753eefffc39328a5cc3133bd446f73`，不是已冻结的最终版本。

## 成果概述

在 WeKnora 原有 RAG 能力上，实现统一插件框架，使第三方扩展可以独立构建、交付和装载，不再为每个新扩展修改核心工厂代码。采用 Manifest + 类型化 gRPC，支持独立进程及 OCI 容器运行路径；内置扩展和外部插件由统一 Manager 管理。

覆盖数据源、文档解析、网络搜索、模型厂商、检索引擎五类扩展。示例以本地目录数据源为主，已在 Windows 完成独立仓库构建、安装、完整同步及单文件增量验收。

## 建议阅读顺序

1. 本文：范围、贡献和交付状态。
2. [成果报告](REPORT.md)：逐项对应原题任务与验收要求。
3. [运行与测试说明](RUNBOOK.md)：环境、命令、演示步骤及预期结果。
4. [验收矩阵](../../plugin/ACCEPTANCE.md)：测试名称和平台边界。
5. [当前架构](../plugin-framework-current-implementation.md)、[插件开发文档](../../plugin/README.md)及[技术路线取舍](../../plugin/ADR-0001-out-of-process-plugin-runtime.md)。

## 关键代码与文档

| 内容 | 位置 |
| --- | --- |
| Manifest、Manager、安装、升级与运行时 | `internal/plugin/` |
| 五类内置扩展统一接入 | `internal/container/container.go` |
| 公共协议、SDK 和模板 | `plugin/proto/`、`plugin/sdk/`、`plugin/templates/` |
| 最小协议示例 | `examples/plugins/` |
| 具体插件独立仓库 | [仓库清单](../../plugin/EXTERNAL-PLUGINS.md) |
| Windows 原生隔离 | `plugin/WINDOWS-NATIVE.md` |
| 宿主受控 HTTP | `plugin/CONTROLLED-HTTP.md` |
| 升级兼容规则 | `plugin/UPGRADES.md` |
| 专有模型推理契约 | `plugin/MODEL-INFERENCE.md` |

## 当前验收结论

- 独立仓库插件装载并完整同步：Windows 已通过。
- 禁止联网并记录实际拒绝：具备管理员 WFP 审计权限的 Windows 已通过；普通权限宿主不保证能够订阅审计事件。
- 单文件增量：Windows 已通过。
- 他人仅依据文档独立实现插件：模板已提供，第三方独立复现尚待完成。
- 中期补充的 Linux/Docker 要求：已有 OCI 与 AppArmor 实现及验收脚本，尚缺 Linux 实机完整运行和审计通过记录。

已有原始证据随主仓保存在 `docs/acceptance/`。本次整理没有重跑全部测试，也没有将尚未完成的事项描述为通过。独立目录插件仓库当前仅存在于本地，正式提交前还需提供可访问的独立远端仓库链接。

## 贡献边界

本次贡献是扩展架构、插件交付、权限执行、生命周期与业务接入。解析、分块、Embedding、索引和 RAG 问答由宿主原有能力承担；验收证明插件能够接通这些能力，不将其作为新算法成果。
