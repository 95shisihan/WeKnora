# WeKnora 外部插件开发（v1alpha1）

技术路线和替代方案见
[`ADR-0001-out-of-process-plugin-runtime.md`](ADR-0001-out-of-process-plugin-runtime.md)，
可执行验收状态见 [`ACCEPTANCE.md`](ACCEPTANCE.md)。

需要访问公开 HTTP API 或链接的插件，请优先使用
[受控 HTTP 开发指南](CONTROLLED-HTTP.md)：插件禁止直接联网，通过 SDK 调用宿主，
由管理员批准域名和方法，由框架检查实际 IP、跳转和限额。Go/Python SDK、
权限字段、编译制品交付及兼容性限制均在该指南中说明。

具体插件源码已迁入独立仓库，仓库清单、构建入口及制品联调方式见[外部插件目录](EXTERNAL-PLUGINS.md)。

本目录定义 WeKnora 与主仓外插件之间的稳定边界。当前已接通的扩展点是
`datasource/v1`、`web_search/v1`、`document_parser/v1`、受限的
`model_provider/v1` 和 `retrieval_engine/v1`；插件可以使用任意支持 gRPC 的语言实现，不能导入
`internal/` 下的 Go 包。

## 运行模型

宿主从 `WEKNORA_PLUGIN_DIRS` 指定的目录发现插件。多个目录使用操作系统的
path-list 分隔符（Linux/macOS 为 `:`，Windows 为 `;`）。每个插件位于一个
独立子目录，入口文件必须名为 `plugin.yaml`：

```text
plugins/
└── local-directory/
    ├── plugin.yaml
    └── bin/
        └── weknora-plugin-local-directory
```

启动时宿主依次执行：

1. 校验 manifest、语义版本范围及 `pluginAPI`；
2. 启动本地 `grpc` 进程，或创建隔离的 `oci` 容器；
3. 调用标准 `grpc.health.v1.Health/Check`；
4. 调用对应类型的 `GetInfo`，核对插件 ID、版本、协议和扩展 ID；
5. 把外部实现注册到与内置实现相同的业务注册表；
6. 将 manifest 的 JSON Schema 暴露给 `/api/v1/datasource/types`，前端据此生成配置字段；
7. 进程退出时关闭 gRPC 连接并删除由宿主管理的进程或容器。

任一步失败都会拒绝装载，不会留下一个看似存在但无法工作的 Connector。

## 管理员上传安装

系统管理员可在“设置 → 插件管理”上传 ZIP 包。普通用户看不到安装和启停入口，
但能够在数据源列表中使用管理员已启用的数据源插件。ZIP 可以直接包含
`plugin.yaml`，也可以多包一层目录；其余文件必须全部位于同一插件目录内。

上传包必须是**构建完成的制品**，不能是源码工程：

```text
my-plugin.zip
├── plugin.yaml
├── bin/
│   └── my-plugin.exe  # Windows；Linux/macOS 使用对应本机可执行文件
└── assets/            # 可选运行资源
```

- `grpc` 插件必须声明 `runtime.command`，入口必须是与宿主操作系统匹配的
  PE/ELF/Mach-O 本机可执行文件；脚本解释器入口不被接受；
- `oci` 插件的 ZIP 只携带 Manifest 和必要资源，`runtime.image` 必须指向已经构建
  好的容器镜像，宿主不会从上传包现场构建镜像；
- `.go`、`.py`、`.js`、`.ts`、`.java`、`.rs`、Shell/PowerShell 等源码脚本，
  以及 `Dockerfile`、`go.mod`、`package.json`、`requirements.txt` 等构建文件会被拒绝。

上传接口为 `POST /api/v1/system/admin/plugins`，multipart 字段名为 `file`。
服务端限制压缩包为 64 MiB、解压后为 256 MiB，并拒绝路径穿越、符号链接、
特殊文件、源码/构建文件、非本机编译入口、重复插件 ID 和不兼容的 Manifest。
校验完成后才会把目录原子移动到
`WEKNORA_PLUGIN_INSTALL_DIR`（本地默认 `./data/plugins`，Docker Compose 默认
持久化到 `/data/plugins`）。新安装插件先登记为停用，管理页再显式调用启用接口，
避免半安装包被执行。

## Manifest

参考[最小数据源模板](templates/datasource-python/plugin.yaml)。具体目录插件的 OCI、Windows 和开发清单由其独立仓库维护。关键字段：

- `metadata.id`：全局稳定 ID，建议使用反向域名；
- `metadata.version`：SemVer；
- `extensionPoints[].id`：写入数据源 `type` 的稳定标识；
- `compatibility.weknora`：宿主版本范围；
- `compatibility.pluginAPI`：当前必须为 `v1`；
- `configSchema`：JSON Schema object。`writeOnly: true` 或 `format: password` 的字段进入凭据存储，其余字段进入 `config.settings`；
- `permissions.network.outbound`：是否申请出站网络；
- `permissions.filesystem`：声明需要的读写范围。

v1alpha1 提供两种运行时：

- `grpc`：本地进程支持 TCP 或 `stdio://` 本地管道，也可连接已有 TCP 服务。
  Windows amd64/arm64 的 `stdio://` 插件可声明 `network.outbound: false`，
  由原生受限访问令牌执行禁网，子进程继承限制，无需容器。
  TCP/外部服务模式仍拒绝不联网声明。详见 [Windows 原生插件](WINDOWS-NATIVE.md)；
- `oci`：生产模式。宿主创建容器，通过只挂载于宿主和插件之间的 Unix Socket
  通信，不发布 TCP 端口。`outbound: false` 会同时应用 Docker `network=none`
  和 `weknora-plugin-no-network` AppArmor profile。根文件系统只读，声明的数据
  目录只读挂载，并默认限制为 256 MiB、1 CPU、128 PID。

v1alpha1 不接受写目录和域名 allowlist：前者尚无安全的租户级生命周期，后者
无法仅靠 Docker bridge 精确执行。宿主会拒绝清单，而不是扩大权限。

### 安装网络审计策略（Linux）

不联网的 OCI 插件采用 fail-closed：如果宿主没有安装 AppArmor profile，Docker
会拒绝创建容器。安装仓库提供的策略：

```bash
sudo apparmor_parser -r deploy/apparmor/weknora-plugin-no-network
```

该策略允许 gRPC 使用的 Unix stream socket，拒绝 IPv4/IPv6 socket，并用
`audit deny` 将每次尝试写入宿主内核审计日志。可使用
`journalctl -k | grep weknora-plugin-no-network` 查看。Docker 的 `network=none`
仍作为第二层隔离存在。macOS/Windows Docker Desktop 不提供等价的宿主 AppArmor
审计，因此严格模式应部署在启用 AppArmor 的 Linux 节点。

在 Linux 验收节点执行下列脚本可完成真实探测，而不只是检查 Docker 创建参数：

```bash
sh plugin/security-probe/verify-linux.sh
```

脚本启动一个实际调用 `connect(2)` 的最小容器，要求连接失败，并检查启动时刻
之后的内核日志中存在该 profile、`DENIED` 和 `family="inet"`。任一条件不满足
都会返回非零状态，适合放入启用 AppArmor 的自托管 CI runner。

## Datasource gRPC 契约

协议源文件为 [datasource.proto](proto/datasource.proto)。插件必须实现：

- `GetInfo`：返回运行时身份；
- `Validate`：校验配置及可访问性；
- `ListResources`：支持资源选择器，可按 `parent_id` 懒加载；
- `ResolveResourceAncestors`：返回选中节点的祖先；
- `Fetch`：服务端流式返回 `FetchedItem`、可选 checkpoint 和最终 cursor。

`config_json` 使用现有 `DataSourceConfig` JSON 形状：

```json
{
  "type": "local_directory",
  "credentials": {},
  "resource_ids": ["docs"],
  "settings": {"root": "/data/manuals"}
}
```

游标由插件拥有，但必须是完整、可恢复的 JSON 快照。外部插件只发现变化并
返回内容，不应直接连接 WeKnora 数据库、解析器或向量库。宿主继续负责文档
解析、分块、Embedding、索引、删除和同步日志。

## 构建并安装外部插件

在[独立插件仓库](EXTERNAL-PLUGINS.md)中构建 ZIP，再通过“设置 → 插件管理”上传和启用。构建和测试命令位于各仓库 README。主仓无须重新编译。

本地目录 Go 版本位于独立目录插件仓库的 `go-plugin/`，执行 `./build.ps1` 生成 Windows ZIP；Linux 镜像在该目录执行 `docker build -t weknora/plugin-local-directory:0.2.0 .`。选择匹配平台的 Manifest，配置只读数据目录后再安装。

## 增量同步约束

参考实现的 cursor 保存 `relative_path -> SHA-256`。每次扫描构造新快照：

- 新路径：发送创建事件；
- 哈希变化：只发送该文件的更新事件；
- 旧快照存在、新快照缺失：发送 `is_deleted=true`；
- 哈希未变：不发送事件，因此不会进入解析和索引链路。

运行验证：

```powershell
# 在独立目录插件仓库的 go-plugin/ 中执行
go test -mod=mod . -count=1
```

测试覆盖“两文件中只修改一个，只发出一个更新”以及删除事件。宿主侧的
`TestPluginIncrementalSyncOnlyReprocessesChangedFile` 会启动一个真实 gRPC
Datasource 服务，经宿主 `datasourcegrpc.Connector` 连续执行两次
`DataSourceService.ProcessSync`。测试使用生产 SQLite `KnowledgeRepository`，断言第二次
同步后数据库仍只有两个有效文档、仅变化文件的知识 ID 与内容哈希更新，并且文件保存和
解析任务入队计数都只增加一次：

```powershell
.tools/go/bin/go.exe test ./internal/application/service -run TestPluginIncrementalSyncOnlyReprocessesChangedFile -count=1
```

Windows 可直接运行完整验收脚本。脚本使用 PATH 中的 Go/GCC，找不到时回退到仓库
`.tools` 目录，并启用 CGO 以编译 `pg_query_go` 和 SQLite：

```powershell
powershell -ExecutionPolicy Bypass -File plugin/test-windows.ps1
```

该脚本依次执行统一 Manager/协议/主仓保留的协议示例插件测试、真实 gRPC 到 SQLite 的增量同步
测试，以及（已安装前端依赖时）Vue TypeScript 类型检查。

Linux CI 的 `.github/workflows/plugin.yml` 还会从 Python 模板目录本身作为完整
Docker build context 构建镜像，防止模板意外依赖主仓文件。该 workflow 的手动
`apparmor-audit` job 会安装安全策略、实际执行一次出站 `connect(2)`，并要求
内核审计日志出现拒绝记录。

## 新插件最小步骤

1. 在独立仓库复制 `plugin/proto/datasource.proto`，为目标语言生成 gRPC 代码；
2. 实现五个 RPC 和标准 gRPC Health；
3. 编写 `plugin.yaml`，使 manifest 身份与 `GetInfo` 完全一致；
4. 给首次同步、无变化、单项变化、删除和无效 cursor 编写测试；
5. 构建可执行文件或自行运行服务；
6. 把插件父目录加入 `WEKNORA_PLUGIN_DIRS`，观察启动日志和 `/api/v1/datasource/types`。

不要复制 WeKnora 的 `internal/types`，也不要让插件执行入库。协议中的 JSON 与
protobuf 才是兼容边界。

## Web Search gRPC 契约

协议源文件为 [web_search.proto](proto/web_search.proto)。插件实现 `GetInfo`、
`Search` 和标准 gRPC Health。宿主把现有 `WebSearchProviderParameters` 序列化到
`parameters_json`，因此凭据仍由 WeKnora 加密存储，插件无需访问数据库。
`Search` 返回标题、URL、摘要、正文、来源及可选发布时间。

manifest 的 `configSchema` 会转换为已有 Web Search 动态表单元数据；
`api_key`、`engine_id`、`base_url`、`proxy_url` 映射到标准参数，其余字段写入
`extra_config`。外部 provider ID 不再需要加入核心枚举或工厂文件。

仓库中的确定性示例可独立构建为 OCI 镜像：

```bash
docker build -f examples/plugins/mock-web-search/Dockerfile \
  -t weknora/plugin-mock-web-search:0.1.0 .
```

该示例声明 `network.outbound: false`，便于同时验证 Web Search 装载与网络隔离；
它不会访问真实搜索服务。将 `examples/plugins` 加入 `WEKNORA_PLUGIN_DIRS` 后，
`mock_search` 会出现在 Web Search provider 类型接口中，并可通过统一管理员接口启停。

## Document Parser gRPC 契约

协议源文件为 [document_parser.proto](proto/document_parser.proto)。`Parse` 使用服务端
流：首帧必须是 Markdown、元数据和错误信息，后续图片与音频数据分别发送，宿主
再转换为现有 `ReadResult`。请求支持文件内容或 URL，并把租户级解析器覆盖配置以
字符串 map 传给插件。

外部解析引擎与内置引擎进入同一个线程安全注册表，因此知识库的 parser rule 可以
直接使用 manifest 的扩展点 ID，无需修改 `engines.go`。示例构建命令：

```bash
docker build -f examples/plugins/plain-text-parser/Dockerfile \
  -t weknora/plugin-plain-text-parser:0.1.0 .
```

示例支持 `txt`、`md` 和 `markdown`，声明不联网，并通过统一 Manager 完成身份校验、
健康检查和动态启停。

## Model Provider gRPC 契约

协议源文件为 [model_provider.proto](proto/model_provider.proto)。模型插件提供厂商元数据、支持的模型类型、默认 URL、动态字段和配置校验，并可选择两种 transport：

- `openai_compatible`：保留原行为，实际请求由宿主兼容客户端发送。
- `grpc_inference`：通过新增 `Infer` / `InferStream` 将 Chat、Embedding、Rerank、VLM、ASR 请求交给插件，专有鉴权、消息和流事件由插件处理。无需逐厂商修改主仓工厂。

公共 SDK、独立脚手架、自定义 HMAC/NDJSON 示例和 Windows 实测范围见[模型推理插件开发](MODEL-INFERENCE.md)。未识别的 transport 仍会被拒绝。

```bash
docker build -f examples/plugins/mock-model-provider/Dockerfile \
  -t weknora/plugin-mock-model-provider:0.1.0 .
```

示例插件本身不联网，只做确定性的配置校验。用户实际配置的模型 Base URL 由宿主
访问，因此该 URL 仍受 WeKnora 现有的模型配置、安全策略和凭据存储约束。

## Retrieval Engine gRPC 契约

协议源文件为 [retrieval_engine.proto](proto/retrieval_engine.proto)，公开 JSON 数据结构
位于 [sdk/retrieval](sdk/retrieval)。插件实现 `GetInfo`、`Upsert`、`Search`、`Delete`、
`Copy`、`Update`、`Estimate` 和标准 gRPC Health。宿主仍负责调用 Embedding 模型，插件
收到的是索引记录及已经生成的向量，因此无需持有模型 API Key。

外部检索引擎与内置引擎进入同一个 `RetrieveEngineRegistry`。在 `RETRIEVE_DRIVER`
加入 manifest 的扩展点 ID 后，租户默认检索配置会根据插件 `GetInfo.support`
生成关键词/向量路由，不需要改核心枚举或 `initRetrieveEngineRegistry()` 工厂：

```bash
export RETRIEVE_DRIVER=memory_plugin
docker build -f examples/plugins/memory-retrieval/Dockerfile \
  -t weknora/plugin-memory-retrieval:0.1.0 .
```

`memory-retrieval` 是完整但非持久化的契约示例，覆盖增量 upsert、关键词/余弦检索、
过滤、三类删除、复制、启停状态和标签更新。生产插件应把相同请求映射到自己的持久化
数据库。外部仓库应依赖公开 `plugin/sdk/retrieval`，不得导入 `internal/types`。

## 可独立仓库模板

没有飞书自建应用时，可使用[独立飞书插件](EXTERNAL-PLUGINS.md)，
匿名导入指定公开页面的文字正文；它不自动遍历知识库，也不支持需登录或分页未完整加载的页面。

真实软件接入教程见 [独立飞书插件仓库](EXTERNAL-PLUGINS.md)：
它用飞书企业自建应用读取知识库 docx 正文，包含授权配置、独立构建、安装、
资源子树选择、哈希增量、删除语义、真实 gRPC 测试和只读真实 API 联调脚本。
可复制整个目录开发其他软件的数据源插件，无需依赖内置飞书连接器。

[`templates/datasource-python`](templates/datasource-python) 是自包含模板，复制
该目录到一个空仓库后即可构建。它拥有自己的 Proto、Python 依赖、服务实现和
Dockerfile；构建上下文不引用 WeKnora 的 `go.mod`、`internal/` 或示例目录。

```bash
cp -R plugin/templates/datasource-python ../my-weknora-plugin
cd ../my-weknora-plugin
git init
docker build -t example/weknora-local-directory:0.1.0 .
```

修改插件时必须同步修改 `plugin.yaml` 的 `metadata.id`、扩展点 ID，以及
`server.py` 中的默认身份。宿主启动时还会通过 `GetInfo` 再核对一次，因此身份
不一致的镜像不能装载。

插件生命周期由统一 `Manager` 持有。状态依次为 `starting`、`healthy`、
`unhealthy`、`stopped`，禁用和当前宿主尚不支持的扩展分别显示为 `disabled`、
`ignored`。业务注册表只接收 Manager 已完成启动和身份验证的数据源适配器。

内置与外部扩展现在都登记到同一个 Manager 状态机。内置实现通过
`BuiltinRegistration` 把业务注册表的发布/摘除回调交给 Manager，外部实现则由
Manager 持有 gRPC/OCI runtime；两者使用同一组管理员启停 API。具有无租户 Health
探针的实现参加周期检查，仅能在获得租户配置后探测的内置连接器会在注册成功时标记
healthy，并在实际业务调用中返回配置相关健康错误。验收状态以
[`ACCEPTANCE.md`](ACCEPTANCE.md) 为准。

SystemAdmin 可以查询和控制当前进程中的插件：

```text
GET  /api/v1/system/admin/plugins
POST /api/v1/system/admin/plugins
POST /api/v1/system/admin/plugins/:plugin_id/enable
POST /api/v1/system/admin/plugins/:plugin_id/disable
```

停用操作先从 Datasource、Web Search、Document Parser、Model Provider 或 Retrieval Engine 业务注册表中摘除扩展，再停止健康
检查和插件运行时；启用操作先完成启动、Health 和身份验证，再注册业务适配器。
当前启停状态是进程级状态，重启后仍以 `plugin.yaml` 的 `spec.enabled` 为准。

Windows 开发机运行 `powershell -ExecutionPolicy Bypass -File plugin/test-windows.ps1`。
脚本会覆盖 manifest/Manager 协议测试、从临时外部目录启动真实子进程 gRPC 插件、
主仓保留的协议示例、两轮 SQLite 增量同步和前端类型检查。AppArmor syscall 拒绝与内核审计
只能在 Linux 验收节点执行；Windows 测试验证 OCI 策略生成和缺少隔离能力时的
fail-closed 行为，不把平台能力缺失伪装成通过。
