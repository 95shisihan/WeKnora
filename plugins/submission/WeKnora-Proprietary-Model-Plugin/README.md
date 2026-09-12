# 专有协议模型插件示例

此示例验证模型请求真正由插件执行。它使用 HMAC-SHA256 认证、`/vendor/infer` 自定义请求和 NDJSON 流，不调用 OpenAI 接口。它对接的是验收测试中的参考服务，不是一个可直接提供真实模型推理的商业厂商。

完整接口、错误/取消规则与权限说明见 [模型插件开发文档](MODEL-INFERENCE.md)。独立脚手架也随附本地 `MODEL-INFERENCE.md`。

## 独立开发

直接在本独立仓库构建。主仓脚手架现在只生成最小协议模板，不再复制本仓库的 HMAC/NDJSON 适配器：

```powershell
$env:CGO_ENABLED = '0'
go build -mod=mod -o bin/proprietary-model.exe .
Compress-Archive -Path plugin.yaml,bin -DestinationPath proprietary-model.zip
```

生成目录包含独立 go.mod、Proto、生成代码、SDK 和适配器源码；不依赖主仓路径或 Go replace。可自行 `git init`。依赖 Go 1.26 和公开 grpc/protobuf 模块。初次构建后可运行 `go mod tidy` 整理依赖并提交 go.mod/go.sum。

1. 修改 `main.go` 和 `plugin.yaml` 中的插件 ID、版本、provider 名称，保持二者一致。
2. 仅在 `ModelTypes` 中声明实际实现的能力。
3. 在 `adapter/backend.go` 中实现目标厂商鉴权、输入/输出转换和流式事件转换。让上游 HTTP 请求使用传入的 context。
4. 在模型设置中选择该 provider，填写模型名、BaseURL、APIKey。这些参数按次调用传给插件，不写入全局变量。
5. 上传 ZIP 并启用，需要包含新增 `grpc_inference` 功能的宿主；旧宿主会拒绝该 transport。

主仓中直接构建本示例的命令是 `go build -o artifacts/proprietary-model.exe ./examples/plugins/proprietary-model`，与独立目录的 `go build ... .` 区分。

## 网络权限

本示例 Manifest 明确声明 `outbound: true`，以直接连接厂商 HTTP/流接口；它不是禁网示例，Windows 下此模式不使用零网络能力身份。只安装可信的模型插件。推理 RPC 本身不会扩大任何插件的权限。

如果插件需要保持禁网，则应声明 `outbound: false`，并将有限的厂商 HTTPS 请求改用已有 `network.http` 宿主代理（需管理员批准）；当前代理是有上限的缓冲响应，不能把它当作实时上游流式接口。本示例没有自动降级或绕过禁网策略。
