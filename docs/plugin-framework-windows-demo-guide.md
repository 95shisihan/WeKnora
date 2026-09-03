# WeKnora 扩展能力插件化框架 Windows 演示手册

> 适用课题：课题一——扩展能力插件化框架  
> 演示环境：Windows PowerShell，WeKnora Lite 0.7.2-dev  
> 项目目录：`E:\Tengxun_RAG\WeKnora`

## 1. 演示目标

本演示用于证明以下能力：

1. 外部插件通过独立 `plugin.yaml` 声明扩展类型、兼容版本、配置项和权限；
2. WeKnora 无需修改核心工厂代码，即可从外部目录发现并装载插件；
3. 外部插件经过启动、gRPC Health、身份校验后，动态注册为数据源；
4. 内置扩展与外部插件共用 Plugin Manager 生命周期；
5. 本地目录插件能够完成首次同步；
6. 只修改一个源文件后，增量同步只重新处理该文件；
7. Windows 可以完成框架和功能演示，但 AppArmor 内核审计需要 Linux 环境。

建议现场演示时间为 12～15 分钟。

## 2. 演示前注意事项

- 所有命令都在普通 PowerShell 中执行；路径中不要加入多余的反斜杠。
- 正确项目路径为 `E:\Tengxun_RAG\WeKnora`。
- 不要直接把 `examples/plugins` 配置为插件目录。该目录还包含多个默认使用 OCI 的示例；Docker 未启动时会影响后端启动。
- 本演示单独创建 `.tmp\plugin-demo\plugins`，其中只放本地目录开发插件。
- 同一个 PowerShell 窗口中完成“设置环境变量”和“启动服务”，否则后端无法继承 `WEKNORA_PLUGIN_DIRS`。
- 演示数据位于 `.tmp`，不会修改项目示例源文件。

## 3. 阶段一：展示自动化验收结果

如果已经运行过，可以直接展示终端截图；需要重跑时执行：

```powershell
cd E:\Tengxun_RAG\WeKnora
powershell -ExecutionPolicy Bypass -File plugin/test-windows.ps1
```

预期最后出现：

```text
Windows plugin acceptance passed.
```

讲解词：

> 这不是单纯的编译检查。脚本测试了统一 Manager、五类插件协议和五个示例，还启动了真实 gRPC 数据源，并通过生产 SQLite KnowledgeRepository 连续执行两次同步，验证只修改一个文件时只新增一次保存和解析任务。

建议截图：

- 五个示例插件测试均为 `ok`；
- `internal/application/service` 为 `ok`；
- 最终的 `Windows plugin acceptance passed.`。

## 4. 阶段二：准备隔离的外部插件目录

打开一个新的 PowerShell，执行以下完整代码块：

```powershell
cd E:\Tengxun_RAG\WeKnora

$demoRoot = "$PWD\.tmp\plugin-demo"
$pluginDir = "$demoRoot\plugins\local-directory"
$dataDir = "$demoRoot\sample-data"

New-Item -ItemType Directory -Force "$pluginDir\bin" | Out-Null
New-Item -ItemType Directory -Force $dataDir | Out-Null

Copy-Item `
  ".\examples\plugins\local-directory\plugin.dev.yaml" `
  "$pluginDir\plugin.yaml" `
  -Force

Set-Content "$dataDir\alpha.txt" "alpha version 1" -Encoding UTF8
Set-Content "$dataDir\beta.txt"  "beta version 1"  -Encoding UTF8

Get-ChildItem $dataDir | Select-Object Name, Length, LastWriteTime
Get-Content "$pluginDir\plugin.yaml"
```

预期看到两个文件：

```text
alpha.txt
beta.txt
```

以及 Manifest 中的关键信息：

```yaml
type: datasource
id: local_directory
protocolVersion: v1
capabilities: [incremental, deletion_sync, hierarchical]
```

讲解词：

> 这个目录可以位于主仓之外。WeKnora 只依赖 Manifest 和 gRPC 协议，不需要导入插件源码，也不需要在核心工厂文件中增加分支。这里使用进程模式便于 Windows 演示；正式生产使用 OCI 模式。

## 5. 阶段三：编译外部数据源插件

继续在同一个 PowerShell 中执行：

```powershell
$env:GOROOT = "$PWD\.tools\go"
$env:GOPATH = "$PWD\.tools\gopath"
$env:GOMODCACHE = "$env:GOPATH\pkg\mod"
$env:GOCACHE = "$PWD\.tools\gocache"
$env:GOTMPDIR = "$PWD\.tools\gotmp"

& "$env:GOROOT\bin\go.exe" build `
  -o "$pluginDir\bin\weknora-plugin-local-directory" `
  ".\examples\plugins\local-directory"

Get-Item "$pluginDir\bin\weknora-plugin-local-directory" |
  Select-Object FullName, Length, LastWriteTime
```

预期结果：生成约十几 MB 的 `weknora-plugin-local-directory` 可执行文件。

如果提示 Go build cache 无权访问，确认已经执行了上述 `GOCACHE` 和 `GOTMPDIR` 设置，再重新执行构建命令。

## 6. 阶段四：让 WeKnora 发现插件并启动界面

仍在同一个 PowerShell 中执行：

```powershell
$env:WEKNORA_PLUGIN_DIRS = "$demoRoot\plugins"

.\scripts\local-dev.ps1 restart -Rebuild
.\scripts\local-dev.ps1 status

Invoke-RestMethod http://127.0.0.1:8080/health
```

预期状态：

```text
backend    running
frontend   running
docreader  running
Frontend: http://127.0.0.1:5174
Backend:  http://127.0.0.1:8080/health
```

检查插件装载日志：

```powershell
Select-String `
  -Path ".\.tools\logs\backend.out.log", ".\.tools\logs\backend.err.log" `
  -Pattern "Plugin|local_directory|io.weknora.local-directory" `
  -CaseSensitive:$false
```

预期至少能找到类似信息：

```text
[Plugin] loaded datasource local_directory from io.weknora.local-directory
```

如果浏览器没有自动打开，手动访问：

```text
http://127.0.0.1:5174
```

建议截图：

- `local-dev.ps1 status` 三项均为 running；
- 后端日志中的插件装载记录。

## 7. 阶段五：在页面中创建知识库和数据源

### 7.1 创建或选择知识库

1. 打开 `http://127.0.0.1:5174`；
2. 创建一个演示知识库，名称建议为“插件化框架演示”；
3. 如果系统要求配置 Embedding 模型，先选择一个当前可用的模型；
4. 进入该知识库的“设置”；
5. 点击左侧“数据源”。

### 7.2 确认插件动态出现

1. 点击“添加数据源”；
2. 在数据源类型列表中找到 `Local Directory`；
3. 截图保留此页面。

讲解词：

> `Local Directory` 不是写死在前端枚举里的。后端从 Manifest 读取扩展元数据和 JSON Schema，通过 `/api/v1/datasource/types` 返回，前端据此动态展示类型和配置字段。

### 7.3 填写数据源配置

建议填写：

| 字段 | 值 |
| --- | --- |
| 名称 | Windows 本地目录演示 |
| Directory / root | `E:\Tengxun_RAG\WeKnora\.tmp\plugin-demo\sample-data` |
| 同步模式 | 增量同步 |
| 同步删除 | 开启 |
| 同步计划 | 任意；现场主要使用“立即同步” |

资源选择页面中选择 `alpha.txt` 和 `beta.txt`，或者不做限制以同步整个目录，然后创建并同步。

## 8. 阶段六：演示第一次完整同步

1. 等待第一次同步结束；
2. 回到知识库文档列表，确认出现 `alpha.txt` 和 `beta.txt`；
3. 打开数据源卡片右上角菜单，进入“同步记录”；
4. 确认第一次同步显示创建 2 项，失败 0 项。

建议截图：

- 文档列表中的两个文件；
- 第一次同步记录中的 `+2` 或创建 2 项；
- 同步状态为成功。

讲解词：

> 插件只发现变化并流式返回原始内容。文件保存、数据库写入、解析、分块、Embedding 和索引仍由 WeKnora 宿主负责，第三方插件不需要数据库权限。

如果文档已经创建但后续解析/索引失败，通常是演示机没有可用的 Embedding 模型。此时插件发现、gRPC 传输和宿主落库仍可展示；完整解析索引需先配置模型。自动化验收不依赖外部模型，仍可证明增量逻辑。

## 9. 阶段七：演示只更新一个文件

回到刚才启动服务的 PowerShell，确保 `$dataDir` 变量仍存在，执行：

```powershell
Set-Content "$dataDir\alpha.txt" `
  "alpha version 2 - changed at $(Get-Date -Format o)" `
  -Encoding UTF8

Get-ChildItem $dataDir | Select-Object Name, Length, LastWriteTime
```

此时应只有 `alpha.txt` 的修改时间发生变化，`beta.txt` 不变。

回到页面：

1. 在数据源卡片右上角菜单点击“立即同步”；
2. 等待同步完成；
3. 打开“同步记录”；
4. 确认第二次同步显示更新 1 项，而不是重新创建或更新 2 项；
5. 打开 `alpha.txt`，确认内容已变为 version 2；
6. 打开或观察 `beta.txt`，确认未变化。

预期核心结果：

```text
第一次：created = 2, updated = 0
第二次：created = 0, updated = 1
beta.txt 未重新处理
```

讲解词：

> 插件游标保存 `relative_path -> SHA-256` 完整快照。第二次扫描时，只有哈希变化的 `alpha.txt` 被发回宿主；`beta.txt` 哈希相同，不产生 FetchItem，因此不会进入解析和索引链路。

建议截图：

- 修改文件前后的 PowerShell 文件列表；
- 第二次同步记录中的 `~1`；
- `alpha.txt` 新内容。

## 10. 可选：演示删除同步

执行：

```powershell
Remove-Item -LiteralPath "$dataDir\beta.txt"
```

在页面再次点击“立即同步”。预期第三次同步显示删除 1 项，知识库中的 `beta.txt` 被删除或标记为已删除。

讲解词：

> 插件会比较旧游标和新快照，对旧快照存在但本次缺失的路径发送 `is_deleted=true` tombstone，由宿主完成数据和索引删除。

该步骤会删除演示目录里的临时文件，不影响项目源码。

## 11. 可选：用测试直接展示统一启停和增量证据

如果现场不方便准备系统管理员账号，可用测试直接展示生命周期状态机：

```powershell
$env:GOROOT = "$PWD\.tools\go"
$env:GOPATH = "$PWD\.tools\gopath"
$env:GOMODCACHE = "$env:GOPATH\pkg\mod"
$env:GOCACHE = "$PWD\.tools\gocache"
$env:GOTMPDIR = "$PWD\.tools\gotmp"

& "$env:GOROOT\bin\go.exe" test -v -count=1 `
  ".\internal\plugin" `
  -run "TestManagerLoadsDisablesAndEnablesDatasourcePlugin|TestManagerRunsBuiltinThroughUnifiedLifecycle|TestExternalDirectoryProcessPluginCompletesIncrementalRoundTrip"
```

预期三个测试均为 `PASS`，分别证明：

- 外部数据源装载、停用和重新启用；
- 内置扩展走统一 Manager 生命周期；
- 主仓外临时目录中的进程插件完成首次及增量 gRPC 往返。

直接展示“单文件只重新处理一次”的宿主验收：

```powershell
powershell -ExecutionPolicy Bypass -File plugin/test-windows.ps1
```

## 12. Windows 演示的边界

Windows 可以证明：

- Manifest 发现与校验；
- 独立进程 gRPC 插件；
- 健康检查与身份握手；
- 动态注册；
- 首次同步、增量更新、删除同步；
- 内置与外部插件统一生命周期；
- 前端动态配置表单。

Windows 不能作为以下验收的最终证据：

- Linux AppArmor 对 `connect(2)` 的实际拒绝；
- Linux 内核审计日志中的 `DENIED` 记录。

因此答辩时应明确表述：

> Windows 演示功能闭环；生产权限隔离由 OCI `network=none` 和 AppArmor `audit deny` 实现，安全验收需要在启用 AppArmor 的 Linux 节点执行。

## 13. Linux 远程服务器安全演示

Linux 服务器应启用 Docker 和 AppArmor，并具有 `sudo` 权限。进入仓库后执行：

```bash
sudo aa-status --enabled
docker info --format '{{json .SecurityOptions}}' | grep -q apparmor
sudo apparmor_parser -r deploy/apparmor/weknora-plugin-no-network
sudo --preserve-env=PATH sh plugin/security-probe/verify-linux.sh
```

预期结果：

1. 探针容器真实调用出站 `connect(2)`；
2. 连接失败；
3. 内核日志出现 `weknora-plugin-no-network`；
4. 日志同时包含 `DENIED` 和 `family="inet"`。

建议把终端完整输出保存为截图或文本制品，作为“禁止联网且尝试被记录”的验收证据。

## 14. 常见问题排查

### 14.1 后端提示没有发现插件

检查环境变量：

```powershell
$env:WEKNORA_PLUGIN_DIRS
Get-ChildItem "$demoRoot\plugins" -Recurse
```

必须存在：

```text
plugins\local-directory\plugin.yaml
plugins\local-directory\bin\weknora-plugin-local-directory
```

设置环境变量后必须在同一个 PowerShell 中重新启动后端。

### 14.2 提示 runtime.command 不是可执行文件

检查输出文件名是否严格为：

```text
weknora-plugin-local-directory
```

Manifest 中的命令是 `bin/weknora-plugin-local-directory`，不要手动改成其他文件名。

### 14.3 端口 50101 被占用

检查占用者：

```powershell
Get-NetTCPConnection -LocalPort 50101 -ErrorAction SilentlyContinue |
  Select-Object State, OwningProcess
```

确认是上一次演示遗留的插件进程后，再执行：

```powershell
$connection = Get-NetTCPConnection -LocalPort 50101 -ErrorAction SilentlyContinue |
  Select-Object -First 1
if ($connection) {
  Stop-Process -Id $connection.OwningProcess
}
```

### 14.4 页面打不开

```powershell
.\scripts\local-dev.ps1 status
Invoke-RestMethod http://127.0.0.1:8080/health
Get-Content ".\.tools\logs\frontend.err.log" -Tail 80
Get-Content ".\.tools\logs\backend.err.log" -Tail 80
```

### 14.5 Local Directory 没有出现在列表中

```powershell
Select-String `
  -Path ".\.tools\logs\backend.out.log", ".\.tools\logs\backend.err.log" `
  -Pattern "Plugin|local_directory|unhealthy|error" `
  -CaseSensitive:$false
```

重点检查：

- `WEKNORA_PLUGIN_DIRS` 是否在启动后端前设置；
- 插件二进制是否存在；
- 50101 端口是否被占用；
- `root` 是否填写为存在的绝对目录。

### 14.6 同步后解析或索引失败

确认：

- DocReader 状态为 running；
- 知识库配置了可用的 Embedding 模型；
- `alpha.txt`、`beta.txt` 使用受支持格式；
- 数据源同步日志中是否有明确错误信息。

## 15. 演示结束后的清理

先停止服务：

```powershell
.\scripts\local-dev.ps1 stop
Remove-Item Env:WEKNORA_PLUGIN_DIRS -ErrorAction SilentlyContinue
```

检查插件端口是否还有遗留进程：

```powershell
$connection = Get-NetTCPConnection -LocalPort 50101 -ErrorAction SilentlyContinue |
  Select-Object -First 1
if ($connection) {
  Stop-Process -Id $connection.OwningProcess
}
```

演示目录可以保留供下次复用。如确定不再需要，可以删除明确的临时目录：

```powershell
Remove-Item -LiteralPath "E:\Tengxun_RAG\WeKnora\.tmp\plugin-demo" -Recurse
```

## 16. 答辩总结话术

> 本项目采用 Manifest、类型化 gRPC 与 OCI 沙箱作为插件边界。插件可在独立仓库中使用任意支持 gRPC 的语言实现，宿主无需修改核心工厂代码。五类扩展共用 Plugin Manager 的发现、装载、启停和健康检查流程，内置扩展也接入同一状态机。示例 Local Directory 数据源通过 SHA-256 游标实现增量同步，源端只修改一个文件时，只有该文件被重新处理。Windows 已完成框架、界面和增量同步演示；生产环境的不联网权限通过 Linux 上的 `network=none` 与 AppArmor `audit deny` 双重执行，并由真实网络探针验证。

## 17. 演示证据清单

演示结束后建议整理以下材料：

- [ ] Windows 验收脚本最终通过截图；
- [ ] Manifest 扩展类型、版本和权限截图；
- [ ] 后端成功装载插件日志；
- [ ] 页面动态出现 Local Directory；
- [ ] 首次同步创建两个文件；
- [ ] 只修改 `alpha.txt` 的 PowerShell 截图；
- [ ] 第二次同步只更新一项；
- [ ] 可选的删除同步截图；
- [ ] Linux 出站连接失败截图；
- [ ] AppArmor `DENIED` 内核日志截图；
- [ ] 本文档及插件开发 README。
