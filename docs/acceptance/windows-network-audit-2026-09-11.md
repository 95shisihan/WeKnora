# Windows 禁网与真实拒绝事件验收

2026-09-11：课题一第二条在具有管理员 WFP 审计权限的 Windows 环境通过。真实受限进程的出站请求被 Windows 拦截，WFP 拒绝事件经宿主生产日志链路记录为 `plugin.network_denied`。

## 实际证据

| 检查 | 结果 |
| --- | --- |
| 原生访问令牌禁网 | `TestWindowsNativeNoNetwork` 通过；父进程及子进程的公网 IPv4/IPv6 TCP 连接被拒绝 |
| 本机代理路径 | 普通进程访问本机 TCP/UDP 正向对照成功；受限进程 TCP 不可连接，UDP 接收端未收到探针数据 |
| 系统拒绝事件 | 14:59:02–14:59:04 的管理员运行取得 4 条真实 WFP CLASSIFY_DROP 事件，目标为 `1.1.1.1:443` 和 `[2606:4700:4700::1111]:443` |
| Manifest 到宿主日志 | 15:03:34–15:03:36 的 `TestWindowsNativeRuntimeDenialAudit` 通过；`outbound: false` 经生产 `startManagedRuntime` 选择原生隔离路径 |
| 连接结果 | 两个公网地址均返回 Winsock `WSAEACCES`（10013），不是以超时当作公网禁网证据 |
| 插件关联日志 | 真实 WFP 回调经过 `runtime_windows.go` 和 `jsonSecurityEventSink`，记录两条 `plugin.network_denied`，`outcome=blocked` |

宿主测试断言：插件 ID、系统 package SID、非空时间、应用路径、协议 TCP/6、端口 443、IPv4/IPv6 地址全部匹配。只出现 `plugin.network_policy_applied` 不会通过。联网探针没有写入任何安全事件，安全事件来源是 Windows。

实际日志节选（完整字段见原始文件）：

```json
{"plugin_id":"io.weknora.windows-audit-probe","action":"plugin.network_denied","outcome":"blocked","details":{"backend":"windows-wfp","protocol":6,"remote_address":"1.1.1.1","remote_port":443}}
```

## 文件与可追溯性

- [系统隔离测试完整输出](windows-network-audit-2026-09-11/windows-network-audit-admin.txt)及[管理员身份、时间和退出码](windows-network-audit-2026-09-11/windows-network-audit-admin-result.json)。
- [宿主运行时测试输出与 Manifest](windows-network-audit-2026-09-11/windows-runtime-audit-admin.txt)及[执行信息](windows-network-audit-2026-09-11/windows-runtime-audit-admin-result.json)。
- [未经改写的宿主安全日志](windows-network-audit-2026-09-11/security-events.txt)及[解析后的 JSON](windows-network-audit-2026-09-11/security-events.json)。
- [普通权限失败记录](windows-network-audit-2026-09-11/non-admin-attempt.txt)：`read Windows network audit configuration: Access is denied.`。
- [宿主验收测试源码](../../internal/plugin/native_audit_windows_test.go)、[独立联网探针](../../plugin/security-probe/windows/main.go)。

测试基线为主仓提交 `c9f50277`。本次只增加测试、探针和证据文档，没有更改生产禁网或审计实现，也没有替换、重启正在运行的 Lite 宿主。

| 文件 | SHA-256 |
| --- | --- |
| 原生隔离测试 EXE | `6e1972ba6c9ebe0b2882680898a2c3f49ccfdfe483769da3abefec6910346992` |
| 宿主运行时测试 EXE | `56a477d697b4b7216f5c15642b1347290fa12da4e2b799e8a1627779a5fd3705` |
| 独立探针 EXE | `9ece95960e2cfb79b9881e57e36648f68d4535b2cc50f01d6163cd860997af10` |
| 宿主验收测试源码 | `5c149ddfff729864b6bfd91a46457dd6e551b6593ee0509f0f6e4f999120b619` |
| 探针源码 | `945597abfc1e10fab79c992f0cbf07e01140373e095c9b0d113242976a057aa3` |

## 复现

在仓库根目录具有 WFP 权限的 PowerShell 中，使用 Go 1.26 和项目现有 Windows GCC 工具链。系统隔离层可运行 `plugin/test-native-windows.ps1 -RequireAudit`。如果 PowerShell 阻止本地脚本，可以直接执行对应 Go 命令；无需更改机器执行策略。

宿主层在本仓库工具布局下的命令如下（环境变量仅作用于当前进程）：

```powershell
$env:GOPATH = "$PWD/.tools/gopath"
$env:GOCACHE = "$PWD/.tools/gocache"
$env:GOTMPDIR = "$PWD/.tools/gotmp"
$env:CGO_ENABLED = '0'
& .tools/go/bin/go.exe build -o artifacts/windows-network-probe.exe ./plugin/security-probe/windows
$env:WEKNORA_WINDOWS_SANDBOX_TEST = '1'
$env:WEKNORA_REQUIRE_WFP_AUDIT = '1'
$env:WEKNORA_NATIVE_AUDIT_PROBE_EXE = (Resolve-Path artifacts/windows-network-probe.exe).Path
$gccBin = "$PWD/.tools/msys64/mingw64/bin"
$compiler = Get-ChildItem .tools/msys64/mingw64/lib/gcc/x86_64-w64-mingw32 -Directory | Sort-Object Name -Descending | Select-Object -First 1
$env:CGO_ENABLED = '1'
$env:CC = "$gccBin/gcc.exe"
$env:CXX = "$gccBin/g++.exe"
$env:COMPILER_PATH = $compiler.FullName
$env:Path = "$gccBin;$($compiler.FullName);$env:Path"
& .tools/go/bin/go.exe test -ldflags "-extldflags=-B$gccBin/" -count=1 -v -timeout=90s ./internal/plugin -run '^TestWindowsNativeRuntimeDenialAudit$'
```

## 边界

管理员测试通过不代表普通权限的 Lite 进程自动获得 WFP 权限。当前普通权限宿主仍可能报告 `attempt_audit: false`；实际部署需要适当权限或另行提供高权限审计服务。后者尚未实现。本次通过 UAC 启动专用验收程序，没有将正在使用的整个 Lite 应用改为管理员运行。

WFP 事件采集是共享系统选项：现有代码在需要时启用采集，结束时关闭本次订阅，不关闭其他程序共享的采集设置。未添加防火墙放行规则、loopback exemption，也未关闭 Windows 防火墙。

此处取得的真实事件是公网 TCP IPv4/IPv6 的拒绝记录；UDP 由接收端非送达及正向对照验证，不声称取得每一次 UDP/loopback 尝试的事件日志。宿主层是生产运行时和日志链路集成测试，专用探针使用管道返回结果，没有实现完整数据源 gRPC 业务服务；完整插件安装/同步由[第一条验收](windows-independent-plugin-2026-09-11.md)独立覆盖。
