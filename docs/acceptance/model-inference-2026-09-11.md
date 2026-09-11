# 专有模型协议插件扩展验收

2026-09-11：新增 `grpc_inference`，模型插件从元数据/配置校验扩展到执行真实模型请求。当前支持聊天及流式、Embedding、Rerank、VLM、ASR 五类入口。旧的 `openai_compatible` 继续使用宿主兼容客户端。

## 验证结果

| 检查 | 结果 |
| --- | --- |
| 旧插件兼容性 | 元数据模式插件正常装载，模型工厂继续调用 OpenAI 兼容 HTTP 并取得结果 |
| 五类工厂路由 | Chat、Embedding、Rerank、VLM、ASR 均经 gRPC 调用插件，由插件发起自定义 HMAC HTTP 请求 |
| Windows 真实独立进程 | 主仓外插件 EXE 复制到外部临时目录，经 Manager 装载、stdio gRPC 通信及重新启用，全部通过 |
| 专有协议转换 | `/vendor/infer` 的 engine/action/arguments 请求、自定义签名认证、reply/encoded/ranked 等响应以及 NDJSON 流事件均成功转换 |
| 聊天完整性 | 推理内容、工具调用及厂商元数据、图片消息、选项、用量、finish_reason 均有验证；thinking 结束不会误终止答案流 |
| 生命周期 | 禁用后已有连接失败，新模型实例明确报不可用；启动时发现禁用插件也不回退宿主 HTTP；重新启用可调用 |
| 并发配置 | 同一插件并发服务两组不同 APIKey/自定义请求头，参考服务的 HMAC 校验均通过 |
| 取消与超时 | 消费端取消及 deadline 从宿主 RPC 传到实际上游 HTTP，请求取消被参考服务观察到 |
| 错误处理 | 上游 403 保留 PermissionDenied 状态码并隐藏响应正文；断流、错误事件、非法 JSON、非法事件类型、超大请求均拒绝 |
| 返回值校验 | 向量条数/维度错误及重排越界索引均报错，不写入伪造成功结果 |
| 开发模板 | 主仓外独立模块可构建；无主仓模块依赖或 replace；生成器保留 Protobuf 描述完整性并拒绝覆盖既有目录 |
| 完整宿主构建 | 项目现有 `scripts/local-dev.ps1 -Action build` 成功生成新的 Lite EXE，未替换或重启正在运行的实例 |

[详细验收日志](model-inference-2026-09-11/acceptance.txt)记录 `TestModelInferenceFactories`、`TestNativeModelInferenceFactories`、`TestModelInferenceFailuresAndCancellation`、`TestInferenceContractRejectsUnsupportedAndMalformedResponses` 及旧插件/禁用状态测试。

[回归日志](model-inference-2026-09-11/regression.txt)覆盖整个 internal/plugin、插件 SDK/Proto 和五类模型包；后续新增的错误契约与旧插件 HTTP 断言见上述详细日志。Python 脚手架回归测试通过，Proto 重新生成前后哈希一致。

## 交付文件

- 开发契约：[MODEL-INFERENCE.md](../../plugin/MODEL-INFERENCE.md)。
- 脚手架：[scaffold_model_plugin.py](../../plugin/scaffold_model_plugin.py)。
- 示例：[proprietary-model](../../examples/plugins/proprietary-model/README.md)。
- 独立 Git 仓库：`E:/Tengxun_RAG/WeKnora-Proprietary-Model-Plugin`，提交 `bd02851491ceb2778fb5173932d9a14b632d1c7a`，尚未发布远端。
- 独立安装包：该独立仓库下 `proprietary-model-windows.zip`，9,208,618 字节。
- 新宿主：主仓下 `artifacts/weknora-model-inference.exe`。使用现有本地开发环境运行，不能将一个新模式插件直接装入仍在运行的旧宿主并期待其自动支持。
- 文件哈希、构建命令与路径：[build.json](model-inference-2026-09-11/build.json)。二进制和 ZIP 是忽略的本地产物，源代码及结构化证据纳入 Git。

## 适用边界

参考 HTTP 服务是可控的专有协议测试端点，返回确定性内容，用来验证转换、传输和错误处理；不是商业模型服务，也不声称完成了任何商业厂商的真实账号/模型质量验收。开发者仍需实现具体厂商协议映射。

示例明确申请 `outbound: true` 以直接访问上游，包括真实流式 HTTP；推理通道不会自动授予联网权限。禁网插件仍受原有机制限制；改用受控 HTTP 时必须遵守其批准流程和当前缓冲响应能力，不能把它当作无限制流式代理。

本次没有更改异常退出身份/ACL 清理，也没有调整普通权限下的 WFP 审计限制。
