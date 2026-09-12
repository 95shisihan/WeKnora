# Windows 原生插件：本地管道与禁网

Windows 10/11 的 amd64、arm64 宿主支持不联网的本机插件进程。插件仍是 `.exe`，不需要 Docker、镜像或虚拟机。业务协议仍是 gRPC，只把传输从本机 TCP 换成匿名管道（stdin/stdout）。

现在也可保持原生进程禁网，并申请管理员批准的[受控 HTTP](CONTROLLED-HTTP.md)：
通过同一 stdio gRPC 连接调用宿主执行 HTTPS。没有申请这项权限的禁网插件仍不能访问网络。

## 配置与 SDK

```yaml
spec:
  runtime:
    type: grpc
    address: stdio://
    command: [bin/weknora-plugin-local-directory.exe]
    startupTimeout: 15s
  permissions:
    network:
      outbound: false
    filesystem:
      read: ["config:settings.root"]
      write: []
```

完整示例是 [独立目录插件仓库](EXTERNAL-PLUGINS.md)中的 `go-plugin/plugin.windows.yaml`。将它作为安装包的 `plugin.yaml`，并附带重新编译的插件程序。旧版只支持 TCP 的二进制必须先更新通信入口。

Go 插件只需要使用 SDK 创建 listener：

```go
import (
    "os"
    "github.com/Tencent/WeKnora/plugin/sdk/transport"
    "google.golang.org/grpc"
)

listener, err := transport.Listen(os.Getenv("WEKNORA_PLUGIN_ADDRESS"))
if err != nil { panic(err) }
server := grpc.NewServer()
// 按原有协议注册业务服务和 gRPC Health 服务。
if err := server.Serve(listener); err != nil { panic(err) }
```

`GetInfo`、配置下发、健康检查、全量和增量同步的 RPC 不变。stdout 专用于 gRPC 二进制流，日志写 stderr；不要在 stdout 打印启动消息。宿主创建和继承管道句柄，插件不监听 IP 端口。连接断开后由管理器重启插件；不能把一条已关闭的标准输入输出连接重新拨号。

SDK 提供 Go stdio listener 和 Python `plugin/sdk/python/stdio_grpc.py` 的 `StdioServer`。Python 适配使用 hyper-h2 在二进制 stdin/stdout 上传输 gRPC，支持健康检查、流量控制、取消和双向宿主 HTTP 通道，不启动 TCP 代理。业务日志必须写 stderr。飞书公开链接插件 0.2.0 已使用该适配并通过真实 Windows 受限 EXE 验证。

## 谁负责禁网

管道只负责通信。真正的禁网由 Windows 访问令牌执行：宿主通过 `CreateProcess` 的安全属性启动不含任何网络能力的原生进程。微软将该机制称为 AppContainer；这里使用的是 Windows 进程权限机制，没有运行容器服务。

宿主在恢复插件线程之前，将它放入 Job Object。子进程继承受限身份；禁用插件时终止整个进程树。插件自行调用 Winsock、其他 HTTP 库或启动自己的子进程，仍受系统网络权限检查。宿主不会添加 loopback exemption，因此插件也不能访问本地 HTTP 代理来转发出站流量。

`outbound: false` 搭配 TCP 地址、没有 `runtime.command` 的远端服务，或不支持原生隔离的平台，会拒绝启动，绝不退回普通进程。`outbound: true` 也可使用 stdio，但管道不会把它变成禁网插件。

参考：[Microsoft：Launch an AppContainer](https://learn.microsoft.com/en-us/windows/win32/secauthz/implementing-an-appcontainer)。

## 文件读取

进程身份每次启动独立生成。宿主只增加该身份对插件目录和声明数据路径的只读权限，退出时撤销自己的 ACL 条目并删除临时身份。不会给 Everyone 或所有应用统一放开目录。

静态路径相对插件目录解析。`config:settings.root` 在宿主发送配置 RPC 前解析和授权，目录必须是绝对本地路径。UNC 路径、网络驱动器被拒绝，以免通过宿主代为访问网络共享。同一运行期已授权目录会缓存，后续同步不重复设置 ACL。首次授权目录时，Windows 可能需要传播继承 ACL；大目录的首次授权耗时应单独测量。

正常关闭会清理身份和授权。宿主被强行结束时，Job Object 会终止插件，但临时身份及其 ACL 条目可能遗留；当前尚无崩溃后遗留身份回收器，不应把这一版本描述为完整桌面安全产品。

## 审计权限与实测证据

2026-09-11 已通过管理员 WFP 真实验收：原生禁网测试收到父/子进程的四条公网 TCP 拒绝事件；宿主运行时测试收到带插件 ID 的两条 `plugin.network_denied` JSON 日志。见[原始记录、复现命令和范围](../docs/acceptance/windows-network-audit-2026-09-11.md)。普通权限进程仍可能无法订阅，不能将管理员验收结果当作普通部署已拥有审计权限。

宿主启动时尝试订阅 Windows Filtering Platform 的拒绝事件，按系统提供的 package SID 关联到插件，包括它的子进程。收到真实拒绝事件后，输出 `plugin-security-audit` JSON 日志，动作为 `plugin.network_denied`，包含插件 ID、目标地址、端口、协议与应用路径。

`plugin.network_policy_applied` 只说明受限身份已应用。它的 `attempt_audit` 字段明确表示是否订阅成功；订阅失败会附上 `audit_error`。普通 Windows 账户可能没有 WFP 审计权限，此时禁网仍然生效，但不能声称已经记录了所有尝试。网络审计依赖适当的 Windows 权限，当前未提供自动安装的高权限审计服务。

审计采用事件订阅，不轮询进程或抓取全部数据包。订阅关闭时不关闭其他程序共享的系统审计配置。UDP 发送 API 可能返回成功而数据包被系统异步丢弃，因此验收要观察接收端，不能仅检查 `Write` 返回值。

## 构建与验收

在仓库根目录，使用 Go 1.26：

```powershell
$env:CGO_ENABLED = '0'
# 先在独立目录插件的 go-plugin/ 运行 build.ps1，宿主测试只接收编译后的 EXE
powershell -File plugin/test-native-windows.ps1
```

脚本实际创建并清理原生受限身份，会短暂增加测试目录的只读 ACL。检查项：

- 原生插件和子进程的 IPv4/IPv6 TCP 访问被系统拒绝；
- 可达的本机 TCP/UDP 端点对普通进程正常，对受限插件不传输数据；
- 管道中二进制数据保持完整，gRPC 健康检查和取消正常；
以下目录联调由宿主 `internal/plugin` 的测试单独执行，需设置 `WEKNORA_NATIVE_DIRECTORY_EXE`：

- 本地目录插件正常读取配置目录，全量同步两个文件；
- 仅修改一个文件后，只返回该文件，再次同步没有变化；
- 如果 WFP 订阅成功，必须收到真实拒绝记录。

需要把补充审计也设为验收门槛时，在具有 WFP 权限的终端执行：

```powershell
powershell -File plugin/test-native-windows.ps1 -RequireAudit
```

通过实际 Manager 的测试还需现有主仓 CGO 工具链（同 `plugin/test-windows.ps1`）：

```powershell
$env:WEKNORA_WINDOWS_SANDBOX_TEST = '1'
$env:WEKNORA_NATIVE_DIRECTORY_EXE = (Resolve-Path '<独立插件构建目录>/bin/weknora-plugin-local-directory.exe').Path
go test -count=1 -v ./internal/plugin -run '^Test(WindowsNativeManagerDirectoryRoundTrip|NativeDirectoryStdioIncremental)$'
```

该测试从独立临时目录装载插件，经过宿主权限校验和 SDK 拨号，验证全量、增量、无变化同步及禁用后重新启用。
