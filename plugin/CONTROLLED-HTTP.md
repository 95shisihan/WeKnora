# 插件受控 HTTP：声明、宿主执行与 SDK

## 交付与使用流程

开发者交付 ZIP：`plugin.yaml`、已编译程序及必要运行库。源码和 SDK 属于开发构建输入，不要求交付用户。系统管理员上传后，申请受控 HTTP 的插件保持停用；管理员查看域名、方法、限额并确认启用。普通用户只填写具体链接或业务配置，不能扩大网络权限。

批准记录由宿主生成，绑定插件 ID、版本和完整权限摘要，保存在插件安装目录的 `.weknora-http-approval`。安装器拒绝包内携带这个文件；插件无写权限。新版本或权限改变会使旧批准失效，宿主重启也不会执行尚未批准的受控插件。当前批准模式是完整接受或拒绝声明，不提供逐条编辑权限界面。管理员可通过重新打包清单收窄范围后安装；同 ID 的新版包请通过插件卡片上的「升级插件」上传，规则见 [UPGRADES.md](UPGRADES.md)。

## 三种联网模式

| 声明 | 行为 |
| --- | --- |
| `outbound: true`，不含 `http` | 兼容旧版直接联网；**不受这里的 HTTP 规则保护** |
| `outbound: false`，不含 `http` | 禁止直接联网，没有宿主 HTTP 通道 |
| `outbound: false`，包含 `http` | 禁止直接联网；管理员批准后，可通过 SDK 请求宿主执行受控 HTTPS |

`outbound: true` 与 `http` 不能同时声明。受控模式只接受受操作系统隔离的 Windows stdio 进程或 OCI/Unix Socket 运行时，不能用于普通 TCP 调试进程。旧的 `network.allow` 字段仍不支持，不能把它误当成新权限。

## 权限清单

```yaml
permissions:
  network:
    outbound: false
    http:
      rules:
        - hosts: [docs.feishu.cn, accounts.feishu.cn, login.feishu.cn]
          methods: [GET]
      maxRedirects: 5
      timeoutSeconds: 20
      maxRequestBytes: 1024
      maxResponseBytes: 2097152
  filesystem:
    read: []
    write: []
```

- `hosts` 必须是小写、精确 DNS 域名，不接受通配符、IP 字面量、端口或 URL。国际化域名需要先转换成 ASCII 域名。允许一个域名表示允许该域名下所有路径；首版没有路径级规则。
- 只支持 HTTPS/443，方法可选 `GET HEAD POST PUT PATCH DELETE OPTIONS`，不支持 CONNECT、TRACE 和任意 TCP。
- `maxRedirects` 为 0～5，默认 0，即不跟随跳转；下一跳必须匹配已批准的域名和方法。

示例包针对本次验收链接额外声明了 `www.feishu.cn` 和 `xiaobot123.feishu.cn`，因为实际页面会依次跳转到这两个域名。其他租户应根据实际用途申请对应的精确域名；宿主不会自动授权新跳转目标。
- 总超时默认 20 秒、上限 30 秒，覆盖 DNS、连接、整个跳转链与读取。SDK 请求可以缩短，不能延长。
- 请求、响应正文各默认 1 MiB，上限 2 MiB。首版缓冲响应，不支持流式大文件、WebSocket、压缩正文自动解压。收到非 identity 的压缩响应会明确报错。
- 每个通道最多 4 个活动请求，宿主最多同时执行 32 个请求。首版没有每用户计费或每分钟请求配额。

## 宿主强制执行

每次请求和每个跳转都重新解析 URL、匹配域名及方法、检查全部 DNS A/AAAA 地址。回环、私有、链路本地、共享地址、常见元数据地址及其他受限范围被拒绝；包含受限地址的混合 DNS 结果也拒绝。连接直接使用已检查的数字 IP，不二次解析域名；TLS 仍验证原域名和证书。

框架不使用宿主环境代理、不继承浏览器 Cookie。请求头仅允许 Accept、Accept-Language、Content-Type、Authorization、User-Agent、If-None-Match、If-Modified-Since，总计上限 16 KiB。另支持严格限定的 Referer：只能是目标 HTTPS 同源根地址（例如 https://docs.qq.com/），禁止路径、查询参数、片段和用户信息；跨域跳转时移除。Host、Cookie、代理头和连接控制头不允许由插件提供。认证头在跨域跳转时清除，跨域跳转携带正文时拒绝；301/302 的 POST 和 303 按标准规则转为 GET，转换后的方法仍需授权。

匿名 Cookie 只在本次请求的跳转链中有效，按标准 Domain/Path/Secure 作用域匹配，并用 Public Suffix List 拒绝 `.com` 等公共后缀 Cookie。跨子域 Cookie 只会发送给作用域匹配且已获准的目标，不能发给未授权域名；这样可支持飞书的匿名访客流程。响应不把 Set-Cookie 交给插件，不同请求不共享 Cookie。宿主目前不代管长期 API 凭据。

安全日志包含宿主绑定的插件 ID、目标主机和结果代码，不记录 URL 路径、查询参数、正文或认证信息。插件不得依赖日志中存在完整 URL。

这套机制限制网络访问，不验证网页正文是否可信，也不阻止插件把它已持有的数据发给已批准的网站。RAG 提示注入、文档解析隔离、业务鉴权以及插件能读哪些文件仍需分别处理。拥有宿主操作系统管理权限的人不属于该插件隔离边界。

## Go SDK

在已有插件服务器上注册服务，无需新增监听端口：

```go
grpcServer := grpc.NewServer()
httpClient := hosthttp.Register(grpcServer)
// 把 httpClient 传给业务服务，然后注册原有 datasource 等服务。
// listener 仍使用 transport.Listen(WEKNORA_PLUGIN_ADDRESS)。
```

业务方法调用：

```go
response, err := httpClient.Do(ctx, hosthttp.Request{
    URL: publicURL,
    Method: "GET",
    TimeoutSeconds: 15,
})
if err != nil {
    return err // 可用 errors.As 提取 *hosthttp.Error.Code
}
if response.StatusCode != 200 {
    return fmt.Errorf("HTTP %d", response.StatusCode)
}
content := response.Body
```

导入公开路径 `github.com/Tencent/WeKnora/plugin/sdk/hosthttp` 与 `plugin/sdk/transport`。不要导入 `internal/plugin/httpbroker`。SDK 不重试请求；尤其 POST 等写请求不能自动重放。调用方必须传入业务 RPC 的 context，使取消可传播。GetInfo、Health 应保持本地操作，不能等待 HTTP 通道；宿主先检查身份和健康，再打开通道。

可编译示例：[独立受控 HTTP 插件](EXTERNAL-PLUGINS.md)。它只演示测试连接，不提供文档同步，业务开发者保留自己的解析、资源列表和 Fetch 实现。此前的禁网验证插件没有申请 `http`，因此仍应失败。

## Python SDK

开发时复制 `plugin/sdk/python/host_http.py`，依赖现有 `grpcio`、`protobuf`：

```python
host_http = HostHTTP(grpc_server)  # 启动服务器前注册
response = host_http.request("GET", public_url, timeout=15)
content = response["body"]
```

`headers` 使用 `{"Accept": ["text/html"]}` 的列表值形式；`body` 为 bytes。`HostHTTPError.code` 为稳定错误码。Python SDK 按 timeout 取消请求；接入业务 RPC 时应取业务剩余时间与本地超时的较小值。

该 SDK 可以注册在 OCI 的 Unix Socket gRPC 服务器上，也可以注册在 `plugin/sdk/python/stdio_grpc.py` 的 `StdioServer` 上。后者通过二进制 stdin/stdout 实现 HTTP/2 gRPC，不使用本地 TCP 代理；支持流量控制、健康检查、取消和双向流。依赖 `h2>=4.3,<5`，日志必须写 stderr。飞书公开链接插件 0.2.0 是完整 Python 接入示例，已编译成自包含 EXE 并通过 Windows 原生禁网进程验收。业务调用可传入 `active=context.is_active`，并将 timeout 限制为业务剩余时间。

```python
from stdio_grpc import StdioServer
from host_http import HostHTTP

server = StdioServer()
host_http = HostHTTP(server)
# 在此按生成的 Proto 注册业务服务和 grpc_health 健康服务。
# 业务服务使用 host_http.request，不直接调用 requests/urllib。
server.start()
server.wait_for_termination()
```

Windows 包仍交付编译 EXE、运行库与声明文件，无需安装 Python 或交付业务源码。
开发者可复制两个 SDK 模块；公开链接模板的构建脚本会在主仓中同步 SDK，复制到独立仓库后使用随模板附带的 SDK。
`WEKNORA_PYTHON` 指向有依赖的解释器后，运行 `go test ./plugin/sdk/transport -run TestPythonStdioGRPC` 验证真实管道互操作。

## 通道协议与生命周期

协议见 `plugin/proto/host_http.proto`。宿主在已验证身份的同一 gRPC 连接上发起双向 `Channel/Open`，SDK 先返回 ready。消息为 protobuf `BytesValue` 中的 JSON：

```json
{"id":1,"request":{"url":"https://docs.feishu.cn/","method":"GET"}}
```

```json
{"id":1,"response":{"statusCode":200,"body":"b2s="}}
```

二进制正文使用 base64，取消为 `{"id":1,"cancel":true}`，错误位于 `response.error` 的 code/message。请求 ID 只用于配对，不代表身份或授权。宿主不接受请求传入的权限、插件身份或代理配置。消息上限 4 MiB，活动 ID 不允许重复。插件停用、通道关闭或请求取消后，宿主停止对应请求；不会自动重放未完成请求。

所有五类扩展点共用通道接入，不必各自实现一套 HTTP 安全逻辑。

## 错误与验收

核心错误包括 `DOMAIN_NOT_ALLOWED`、`TARGET_IP_DENIED`、`REDIRECT_DENIED`、`HEADERS_DENIED`、`REQUEST_TOO_LARGE`、`RESPONSE_TOO_LARGE`、`ENCODING_DENIED`、`TIMEOUT`、`CANCELLED`、`BUSY` 和 `NETWORK_ERROR`。真实 HTTP 4xx/5xx 作为响应返回，由插件解释，不能全部当成权限拒绝。

```powershell
./plugin/test-windows.ps1
# 在 WeKnora-ControlledHTTP-Plugin 独立仓库执行 ./build.ps1
```

单独运行网络策略和 Go SDK 通道测试：

```text
go test ./internal/plugin/httpbroker ./plugin/sdk/...
```

Python SDK 协议测试：在 `plugin/sdk/python` 下运行 `python -m unittest -v`。

原生 Windows 验收设置 `WEKNORA_WINDOWS_SANDBOX_TEST=1`、`WEKNORA_CONTROLLED_HTTP_EXE` 指向构建 EXE，并运行 `go test ./internal/plugin -run '^TestWindowsNativeControlledHTTP$'`。可选 `WEKNORA_FEISHU_TEST_URL` 指定真实公开链接；宿主测试进程必须允许联网。OCI 通道沿用 Unix Socket，但本机未提供 Docker 实机验收时不能声称已经通过 OCI 端到端测试。
