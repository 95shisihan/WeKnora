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

### 1.1 正式演示的呈现原则

本次演示分成两个阶段：

- **评委入场前的准备阶段**：编译插件、设置插件目录、启动 WeKnora。这些命令不作为主演示内容；
- **评委看到的正式阶段**：全程以浏览器为主，只在修改源文件时短暂打开记事本。评委应亲眼看到插件出现在页面、连接成功、资源列表、首次同步结果和单文件增量结果。

正式演示时不要从头运行耗时的验收脚本。验收脚本的通过截图放在最后作为自动化证据即可。

### 1.2 评委在前端能看到什么

| 评委看到的页面现象 | 对应证明 |
| --- | --- |
| “添加数据源”中出现 `Local Directory` | 外部插件被动态发现并注册，无需修改主仓工厂 |
| 四步抽屉自动出现 `Directory` 字段及说明 | 前端表单来自插件 Manifest 的 JSON Schema |
| 点击“测试连接”后显示“连接成功” | 前端经宿主调用外部插件的 gRPC `Validate` |
| “选择范围”中出现两个本地文件 | 宿主调用插件的 gRPC `ListResources` |
| 首次同步卡片显示 `+2` | 插件完成首次全量发现和宿主入库 |
| 第二次同步卡片显示 `~1` | 仅变化文件进入重新处理链路 |
| 同步历史中两次任务分别为创建 2、更新 1 | 增量同步结果有可追踪证据 |
| 文档页面从 V1 变成 V2，另一文件不变 | 变化内容真正写入宿主，而非只更新计数 |

### 1.3 当前前端边界

当前已有统一插件启停和健康状态管理员 API，但还没有单独的“插件管理”前端页面。因此：

- 浏览器主演示覆盖插件动态注册、配置、连接、资源发现、首次同步和增量同步；
- 统一启停通过自动化测试或管理员 API 作为补充证据；
- 不要向评委声称页面中已有插件启停按钮。

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

Set-Content "$dataDir\alpha.txt" @(
  "插件化框架演示文档 Alpha"
  "版本：V1"
  "状态：首次同步内容"
  "说明：稍后只修改本文件，用于验证单文件增量同步。"
) -Encoding UTF8

Set-Content "$dataDir\beta.txt" @(
  "插件化框架演示文档 Beta"
  "版本：V1"
  "状态：对照文件"
  "说明：第二次同步时本文件保持不变，不应被重新处理。"
) -Encoding UTF8

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

## 7. 正式演示开始：先向评委说明场景

评委开始观看后，把 PowerShell 最小化，浏览器打开：

```text
http://127.0.0.1:5174/platform/knowledge-bases
```

建议开场用时 30 秒，讲解词：

> 我现在演示的是位于外部目录的数据源插件。它没有加入 WeKnora 的数据源枚举，也没有修改核心工厂。宿主启动时读取插件 Manifest，通过 gRPC 健康检查和身份校验后，把它动态注册到现有数据源体系。接下来所有关键结果都会直接在 WeKnora 页面中展示。

演示桌面建议：

- 浏览器占屏幕约 80%；
- 文件资源管理器或记事本提前打开演示数据目录，占剩余区域或保持在任务栏；
- PowerShell 最小化，只在出现问题时使用；
- 浏览器缩放建议 90%～100%，确保四步抽屉和同步指标完整显示。

## 8. 前端场景一：创建演示知识库

### 8.1 从知识库列表进入

1. 浏览器进入“知识库”列表；
2. 点击页面标题“知识库”旁边的“新建知识库”图标；
3. 在“新建知识库”抽屉中选择“文档”类型；
4. 名称填写：`插件化框架演示`；
5. 描述填写：`外部 Local Directory 插件首次同步与增量同步演示`；
6. 保持默认存储和分块设置；
7. 如果页面要求选择 Embedding 模型，选择演示机上已经测试可用的模型；
8. 点击右下角“创建”。

创建成功后，当前实现会继续停留在知识库设置抽屉，并从“创建模式”进入“编辑模式”。左侧“存储与数据”分组此时会新增“数据源”入口。

如果知识库已经提前创建：

1. 在知识库卡片右上角点击“更多”；
2. 点击“知识库设置”；
3. 在左侧“存储与数据”分组点击“数据源”。

建议截图点 A：

- 知识库设置左侧已经出现“数据源”；
- 知识库名称为“插件化框架演示”。

讲解词：

> 数据源属于具体知识库。这里先建立一个干净的演示知识库，后续两个文档都由外部插件同步进入，而不是通过上传按钮添加。

## 9. 前端场景二：让评委看到外部插件动态出现

1. 在知识库设置左侧点击“数据源”；
2. 页面标题应为“数据源管理”，说明文字为“配置外部数据源，自动同步内容到知识库”；
3. 点击“添加数据源”卡片；
4. 右侧打开“添加数据源”四步抽屉；
5. 顶部依次显示“选择类型 → 配置凭证 → 选择范围 → 同步策略”；
6. 在类型列表中找到 `Local Directory`；
7. 暂时不要点击，先让评委看到它与飞书、Notion、GitLab 等内置连接器处于同一选择页面；
8. 指出它的说明文字：同步只读本地目录。

建议截图点 B：

- 四步进度条完整显示；
- `Local Directory` 卡片与内置数据源同时显示。

讲解词：

> `Local Directory` 不是前端写死的类型。后端从外部插件 Manifest 读取名称、描述、能力和 JSON Schema，通过统一的数据源类型接口返回，所以它能和内置连接器出现在同一个页面。移除插件后，这个选项也会随注册表摘除，不需要重新编译前端。

## 10. 前端场景三：演示 Manifest 驱动的动态配置

### 10.1 第一步——选择类型

点击 `Local Directory` 卡片。抽屉自动进入第二步“配置凭证”。

### 10.2 第二步——配置和测试连接

建议填写：

| 字段 | 值 |
| --- | --- |
| 名称 | Windows 本地目录演示 |
| Directory / root | `E:\Tengxun_RAG\WeKnora\.tmp\plugin-demo\sample-data` |

操作顺序：

1. 保留自动填入的名称 `Local Directory`，或改成“Windows 本地目录演示”；
2. 在 `Directory` 字段填写上述绝对路径；
3. 指向字段下方的说明文字，告诉评委该字段来自 `plugin.yaml` 的 `configSchema`；
4. 点击左下角“测试连接”；
5. 等待按钮旁出现绿色图标和“连接成功”；
6. 在连接成功状态停留 2～3 秒，便于评委观察和截图；
7. 点击右下角“下一步”。

建议截图点 C：

- `Directory` 动态字段；
- 完整目录路径；
- “连接成功”状态。

讲解词：

> `Directory` 不是 Local Directory 专用前端代码，而是 Manifest JSON Schema 动态生成的字段。点击测试连接后，请求经过宿主统一接口转到插件的 gRPC `Validate`；插件实际检查目录是否存在且可读。

### 10.3 第三步——从插件加载资源

进入“选择范围”后：

1. 等待资源列表加载完成；
2. 页面应显示 `alpha.txt` 和 `beta.txt`；
3. 点击两行，确保两项均显示选中状态；
4. 页面顶部的已选数量应显示 2；
5. 点击“下一步”。

建议截图点 D：

- 插件返回的两个文件；
- 已选数量为 2。

讲解词：

> 这一步不是浏览器读取本地文件。前端请求宿主，宿主再调用插件的 gRPC `ListResources`。也就是说，资源树同样走统一插件协议。

### 10.4 第四步——配置增量同步策略

在“同步策略”页面设置：

| 前端选项 | 选择值 |
| --- | --- |
| 同步频率 | 每 6 小时，保持默认即可 |
| 同步模式 | 增量同步 |
| 冲突策略 | 覆盖更新 |
| 同步删除 | 勾选 |

确认页面后先停留数秒，向评委解释“增量同步”和“同步删除”，然后点击“创建并立即同步”。

页面应提示：

```text
数据源创建成功，同步任务已提交
```

## 11. 前端场景四：展示第一次完整同步

### 11.1 在数据源卡片上观察

1. 创建抽屉关闭后回到“数据源管理”；
2. 页面出现“Windows 本地目录演示”数据源卡片；
3. 同步期间卡片显示“同步中”；
4. 等待页面自动刷新，或约 3 秒后观察状态；
5. 成功后卡片应显示“成功”和绿色 `+2` 指标；
6. 卡片副标题应显示 `Local Directory · 增量同步 · 已连接`。

### 11.2 展开同步历史

1. 点击数据源卡片右上角“三点”菜单；
2. 点击“日志”；
3. 右侧打开“同步历史 · Windows 本地目录演示”；
4. 确认汇总中的成功次数为 1；
5. 展开最新一条记录；
6. 确认状态“成功”、总计 2、创建 2、失败 0；
7. 关闭同步历史抽屉。

### 11.3 在知识库中检查文档内容

1. 保存并关闭知识库设置；
2. 点击进入“插件化框架演示”知识库；
3. 在文档列表确认出现 `alpha.txt` 和 `beta.txt`；
4. 点击 `alpha.txt`，确认页面能看到：`版本：V1` 和“首次同步内容”；
5. 返回列表并点击 `beta.txt`，确认它显示：`状态：对照文件`。

建议截图：

- 数据源卡片的绿色 `+2`；
- 同步历史的“创建 2、失败 0”；
- 文档列表中的两个文件；
- `alpha.txt` 的 V1 内容。

讲解词：

> 插件只发现变化并流式返回原始内容。文件保存、数据库写入、解析、分块、Embedding 和索引仍由 WeKnora 宿主负责，第三方插件不需要数据库权限。

如果文档已经创建但后续解析/索引失败，通常是演示机没有可用的 Embedding 模型。此时插件发现、gRPC 传输和宿主落库仍可展示；完整解析索引需先配置模型。自动化验收不依赖外部模型，仍可证明增量逻辑。

## 12. 前端场景五：可视化演示只更新一个文件

这一段不要用脚本静默修改。提前在文件资源管理器中打开：

```text
E:\Tengxun_RAG\WeKnora\.tmp\plugin-demo\sample-data
```

让评委看到目录中只有 `alpha.txt` 和 `beta.txt` 两个文件，然后：

1. 双击用记事本打开 `alpha.txt`；
2. 把 `版本：V1` 修改为 `版本：V2`；
3. 把 `状态：首次同步内容` 修改为 `状态：仅 Alpha 已更新`；
4. 增加一行：`验收结论：第二次同步只应更新本文件。`；
5. 按 `Ctrl+S` 保存并关闭记事本；
6. 不要打开或修改 `beta.txt`。

此时应只有 `alpha.txt` 的修改时间发生变化，`beta.txt` 不变。

回到页面：

1. 打开“插件化框架演示”知识库卡片右上角菜单；
2. 点击“知识库设置”；
3. 左侧进入“数据源”；
4. 在“Windows 本地目录演示”卡片右上角点击“三点”；
5. 点击“立即同步”；
6. 页面提示“同步任务已提交”，卡片短暂显示“同步中”；
7. 等待状态恢复为“成功”；
8. 观察卡片指标：应显示紫色或对应样式的 `~1`，不应是 `~2`；
9. 打开“日志”；
10. 确认现在共有两次成功记录；
11. 最新记录为“更新 1、创建 0、删除 0、失败 0”；
12. 上一条记录仍为“创建 2”。

最后验证内容：

1. 关闭设置并回到知识库文档列表；
2. 打开 `alpha.txt`，确认已经显示 `版本：V2`；
3. 确认新增的“验收结论”一行已经出现；
4. 打开 `beta.txt`，确认仍为 `版本：V1` 和“对照文件”。

预期核心结果：

```text
第一次：创建 2、更新 0、删除 0
第二次：创建 0、更新 1、删除 0
alpha.txt：V1 → V2
beta.txt：保持 V1
```

讲解词：

> 插件游标保存 `relative_path -> SHA-256` 完整快照。第二次扫描时，只有哈希变化的 `alpha.txt` 被发回宿主；`beta.txt` 哈希相同，不产生 FetchItem，因此不会进入解析和索引链路。页面上的 `~1` 和同步历史中的“更新 1”，就是这一机制的直接可见结果。

建议截图：

- 记事本中把 Alpha 从 V1 改为 V2；
- 第二次同步记录中的 `~1`；
- 两次同步历史对比；
- `alpha.txt` 的 V2 新内容；
- `beta.txt` 仍为 V1。

## 13. 主演示结束：用 40 秒完成总结

在第二次同步历史或 Alpha V2 内容页面停留，使用以下总结：

> 刚才的 Local Directory 来自主仓外的插件目录。它通过 Manifest 声明数据源类型、兼容版本、配置 Schema 和权限，通过类型化 gRPC 提供连接验证、资源枚举和流式同步。第一次同步创建两个文档；源端只修改 Alpha 后，第二次同步在前端明确显示只更新一项，Beta 内容保持不变。这证明插件无需修改主仓即可装载，也证明增量游标阻止未变化文件重新进入解析和索引流程。

至此浏览器主演示结束。自动化测试截图和 Linux 网络隔离证据放在问答环节展示。

## 14. 可选加分项：在前端演示删除同步

执行：

```powershell
Remove-Item -LiteralPath "$dataDir\beta.txt"
```

在页面再次点击“立即同步”。预期第三次同步的数据源卡片显示 `-1`，同步历史显示删除 1 项，知识库中的 `beta.txt` 被删除或标记为已删除。

讲解词：

> 插件会比较旧游标和新快照，对旧快照存在但本次缺失的路径发送 `is_deleted=true` tombstone，由宿主完成数据和索引删除。

该步骤会删除演示目录里的临时文件，不影响项目源码。

## 15. 补充证据：统一启停和增量测试

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

## 16. Windows 演示的边界

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

## 17. Linux 远程服务器安全演示

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

## 18. 常见问题排查

### 18.1 后端提示没有发现插件

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

### 18.2 提示 runtime.command 不是可执行文件

检查输出文件名是否严格为：

```text
weknora-plugin-local-directory
```

Manifest 中的命令是 `bin/weknora-plugin-local-directory`，不要手动改成其他文件名。

### 18.3 端口 50101 被占用

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

### 18.4 页面打不开

```powershell
.\scripts\local-dev.ps1 status
Invoke-RestMethod http://127.0.0.1:8080/health
Get-Content ".\.tools\logs\frontend.err.log" -Tail 80
Get-Content ".\.tools\logs\backend.err.log" -Tail 80
```

### 18.5 Local Directory 没有出现在列表中

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

### 18.6 同步后解析或索引失败

确认：

- DocReader 状态为 running；
- 知识库配置了可用的 Embedding 模型；
- `alpha.txt`、`beta.txt` 使用受支持格式；
- 数据源同步日志中是否有明确错误信息。

## 19. 演示结束后的清理

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

## 20. 答辩总结话术

> 本项目采用 Manifest、类型化 gRPC 与 OCI 沙箱作为插件边界。插件可在独立仓库中使用任意支持 gRPC 的语言实现，宿主无需修改核心工厂代码。五类扩展共用 Plugin Manager 的发现、装载、启停和健康检查流程，内置扩展也接入同一状态机。示例 Local Directory 数据源通过 SHA-256 游标实现增量同步，源端只修改一个文件时，只有该文件被重新处理。Windows 已完成框架、界面和增量同步演示；生产环境的不联网权限通过 Linux 上的 `network=none` 与 AppArmor `audit deny` 双重执行，并由真实网络探针验证。

## 21. 演示证据清单

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
