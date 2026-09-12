# 专有协议模型插件开发

2026-09-11 新增 `grpc_inference`，让外部插件直接执行模型请求。模型适配器仍通过统一 Manager 启停与健康检查装载。

## 两种模式

| GetInfo.transport | 插件负责 | 宿主负责 |
| --- | --- | --- |
| `openai_compatible` | 厂商元数据、动态配置项、配置校验 | 使用原有兼容客户端调用模型 |
| `grpc_inference` | 元数据、校验，以及真实鉴权、请求、响应和流式协议转换 | 调用统一 gRPC，接回业务结果 |

现有两个元数据 RPC 不变；新增 `Infer` 和 `InferStream`，沿用 `model_provider/v1` 的增量兼容协议。老插件不用实现新 RPC。旧宿主会拒绝新 transport，不能把模型名称改成 `generic` 来代替适配。

## 调用契约

`ModelInferenceRequest` 包含类型化 `operation`、`config_json` 与 `input_json`。结果为 `output_json`。这是 WeKnora 自己的公共契约，不要求厂商提供 OpenAI API。Go 类型见公开的 `plugin/sdk/model`；脚手架中的位置是 `sdk/model`。其他语言可以从 `model_provider.proto` 生成 gRPC 代码。

| operation | ModelTypes 声明 | 输入对象 | 输出对象 |
| --- | --- | --- | --- |
| CHAT | KnowledgeQA | `ChatInput{messages, options}` | `ChatOutput{content, reasoning_content, tool_calls, finish_reason, usage}` |
| EMBED | Embedding | `EmbedInput{texts, dimensions, supports_dimension_override, truncate_prompt_tokens}` | `EmbedOutput{vectors}`，顺序和条数对应输入 |
| RERANK | Rerank | `RerankInput{query, documents}` | `RerankOutput{results:[{index, relevance_score}]}`，index 指向原文档 |
| VISION | VLLM | `VisionInput{images, prompt}` | `TextOutput{text}` |
| TRANSCRIBE | ASR | `TranscribeInput{audio, file_name, language}` | `TranscribeOutput{text, segments:[{start,end,text}]}` |

图片和音频是 JSON base64 二进制，不传宿主本地文件路径。只有 CHAT 支持 `InferStream`。不支持的操作必须返回 gRPC `UNIMPLEMENTED`，不得伪造空成功结果。

`config_json` 每次包含当前模型的 `model_name`、`model_id`、`base_url`、`api_key`、可选 `extra` 和 `custom_headers`。自定义签名所需字段通过 `extra` 声明并读取。不会自动转发宿主其他模型的密钥、环境变量或内置厂商专属凭证。不要全局保存配置，同一插件进程可能并发服务多个租户/模型。日志不要输出请求配置、签名密钥或上游认证失败响应正文。

聊天消息支持 system/user/assistant/tool、文本、multi_content 图片、images、tool_call_id、完整 tool_calls、reasoning_content。工具调用 `function.arguments` 是 JSON 字符串；`provider_metadata` 可保存厂商需要原样回传的签名/状态。它不是宿主内部执行状态。

`options` 对应当前 ChatOptions：temperature、top_p、seed、max_tokens、max_completion_tokens、frequency_penalty、presence_penalty、thinking、tools、tool_choice、parallel_tool_calls、format。tools 的 function 含 name、description、parameters（JSON Schema）；format 也是 JSON 对象。插件负责映射厂商参数；不支持而又影响语义的选项应返回明确错误，不能静默丢弃。SDK保留 JSON 形式，避免把专有选项转换逻辑写入宿主。

## 流式输出

输出帧为 `StreamOutput`，仅允许 `answer`、`thinking`、`tool_call`、`error` 四类 `response_type`。

```json
{"response_type":"thinking","content":"分析片段","done":false}
{"response_type":"thinking","content":"","done":true}
{"response_type":"answer","content":"答案片段","done":false}
{"response_type":"answer","content":"","done":true,"finish_reason":"stop","usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10}}
```

thinking 的 done 只结束思考段。整个流必须以一次 answer/error 的 done=true 结束。未收到终止帧就断开会返回错误，不能作为成功；收到终止帧后宿主关闭上游 RPC。工具调用必须先在插件中合并成完整调用，再输出 tool_call 帧；不能将半段 JSON 参数交给宿主执行。Usage 字段可携带缓存读写统计。

消费端取消或超时会关闭 RPC 并传到插件 context；插件必须继续传给 HTTP/SDK，并及时停止读取/发送。消息通过有界缓冲传递，不先收齐上游所有内容。宿主调用上限为 5 分钟，调用方更短的 deadline 优先。消费者停止读流时必须取消 context。

## SDK 与开发步骤

嵌入 `model.UnimplementedBackend`，按需要实现 `ValidateConfig`、`Infer`、`InferStream`，然后用 `model.Server{Info: ..., Backend: ...}` 注册 ModelProviderPlugin。注册 gRPC Health 服务并通过 `transport.Listen` 支持 stdio。完整可编译入口和自定义协议实现位于 `examples/plugins/proprietary-model`。

生成主仓外独立模板：

```powershell
python plugin/scaffold_model_plugin.py E:/plugins/my-model --module example.org/my-model
```

目标目录必须不存在，脚手架不会覆盖已有代码。生成内容包含 Proto、生成代码、模型 SDK、管道 SDK和参考适配器；无需修改主仓的模型工厂文件。

## 大小、错误与生命周期

- gRPC 服务端需配置 `MaxRecvMsgSize` / `MaxSendMsgSize` 为 SDK 的 32 MiB，客户端同样限制；请求总 JSON 留出 1 KiB 协议开销。音频/图片 base64 后也计入限制，大文件需由插件开发者设计分段业务，当前不支持上传分片。
- 返回结构必须是 JSON 对象；关键输出字段缺失、向量条数/维度不符、重排索引越界或重复会被拒绝。
- 通过标准 gRPC 状态码报告认证、限额、不可用、超时、取消等错误。宿主保留状态码但替换任意上游错误正文，避免把密钥或响应调试信息展示给用户。
- 插件禁用后，已有实例的 gRPC 连接关闭；新建实例明确报不可用，不回退到宿主 HTTP。重新启用后新建模型实例即可使用。发现了尚未启用的模型插件时也会保留其厂商名，避免错误回退。
- 原 OpenAI 兼容插件保持原调用方式。推理能力不会改变 Manifest 的联网权限；专有协议可用直接网络模式，或在其能力范围内使用受控 HTTP。网络隔离和异常退出清理机制本次没有更改。

## 已执行验证与边界

Windows 的 `TestNativeModelInferenceFactories` 把真实独立 EXE 复制到外部临时插件目录，经 Manager 和 stdio gRPC 调用自定义 HMAC/NDJSON HTTP 服务，验证五类工厂、工具/推理/用量、并发凭证与禁用/重启。`TestModelInferenceFailuresAndCancellation` 验证断流、错误、取消传递到 HTTP、deadline、错误向量和重排索引。

这是专有协议扩展能力的真实进程验证，参考服务用于协议测试，不是商业模型质量测试。某个具体商业厂商仍需独立实现映射并用其真实账号验收。当前正在运行的旧 Lite EXE 不会自动获得源码变更，需要使用新构建的宿主后才能安装新模式插件。
