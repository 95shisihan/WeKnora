# 飞书知识库外部插件：从独立仓库到增量同步

**无需应用凭据的公开链接导入**：请使用独立的公开链接插件，见
[公开链接安装与开发说明](PUBLIC_LINKS.md)。下面说明的是企业自建应用 API 模式。

这是一个可复制到独立仓库的 WeKnora `datasource/v1` 插件，同时作为真实软件接入教程。
插件通过飞书开放 API 读取知识库，经 gRPC 返回文档；WeKnora 负责解析、分块、向量化、索引。
它不导入主仓代码，不依赖内置飞书连接器，不需要新增核心注册代码。

## 1. 示例范围与前提

- 使用中国版飞书**企业自建应用**的 App ID / App Secret，运行时获取 tenant_access_token。
- 每个数据源绑定一个知识空间，可同步整个空间或所选页面及其全部后代。
- 只导入新版云文档 `obj_type=docx` 的纯文本正文。旧版 doc、Sheet、Bitable、图片、附件及富文本格式不在本示例范围；其他类型节点仍可作为父节点遍历。
- 每轮读取所选 docx 正文，比较标题与正文的 SHA-256，**仅变化文档重新交给宿主处理**。这是处理增量，不承诺只调用一次飞书 API。
- 不使用编辑时间判定内容是否变化，避免同一时间精度内的修改被漏掉。
- 支持分页、令牌刷新、有限重试、删除标记、健康检查和资源祖先查询。
- 示例上限：每空间 2000 个节点、每文档正文 2 MiB、单轮变化正文累计 64 MiB。更大规模需要持久化暂存或安全 checkpoint 设计。
- 同一插件实例一次只接受一个 Fetch；并发同步返回 `RESOURCE_EXHAUSTED`，稍后重试。健康检查可继续响应。
- 生产 OCI 模式建议 Linux Docker 宿主；Windows 可以先使用本地 TCP gRPC 调试。

## 2. 在飞书侧准备授权

1. 在飞书开放平台创建企业自建应用，记录 App ID 和 App Secret，启用机器人能力以便按知识库的授权流程授予应用访问权。
2. 在应用权限管理申请知识库读取及文档正文读取权限。常用权限为 `wiki:wiki:readonly`、`docx:document:readonly`；以当前控制台及下方各 API 页的可选权限列表为准。
3. 发布应用版本，并完成租户管理员要求的审批。修改权限后也要使新版本生效。
4. 按知识库管理界面提供的方式把应用/机器人加入目标知识空间并授予读取权限。仅申请 API scope 不等于取得具体文档访问权限。
5. 获取目标 `space_id`。它是知识空间 ID，不是 `/wiki/<node_token>` 中的页面 token；可以使用飞书 API 调试台的知识空间列表/节点信息接口查看。
6. 确保所选节点及后代文档对应用可读。`Validate` 只检查知识空间访问；正文权限会在同步时检查。

官方契约（核对日期 2026-09-09；接口网页可能需要登录后查看权限详情）：

- [获取自建应用 tenant_access_token](https://open.feishu.cn/document/server-docs/authentication-management/access-token/tenant_access_token_internal)
- [获取知识空间列表](https://open.feishu.cn/document/server-docs/docs/wiki-v2/space/list)
- [获取知识空间信息](https://open.feishu.cn/document/server-docs/docs/wiki-v2/space/get)
- [获取知识空间子节点列表](https://open.feishu.cn/document/server-docs/docs/wiki-v2/space-node/list)
- [获取知识空间节点信息](https://open.feishu.cn/document/server-docs/docs/wiki-v2/space/get_node)
- [获取文档纯文本内容](https://open.feishu.cn/document/server-docs/docs/docx-v1/document/raw_content)

不要把 Secret 写进 Manifest、源码、Docker 镜像或版本库。生产凭据在 WeKnora 数据源表单填写。

## 3. 复制到独立仓库并运行测试

以下从 WeKnora 根目录出发，Linux/macOS 示例：

```bash
cp -R plugin/templates/feishu-wiki-python ../my-feishu-plugin
cd ../my-feishu-plugin
git init
python3 -m venv .venv
. .venv/bin/activate
python -m pip install -r requirements.txt
python -m grpc_tools.protoc -I. --python_out=. --grpc_python_out=. datasource.proto
python -m unittest -v
```

Windows PowerShell：

```powershell
Copy-Item -Recurse plugin/templates/feishu-wiki-python ../my-feishu-plugin
Set-Location ../my-feishu-plugin
git init
python -m venv .venv
.venv/Scripts/python.exe -m pip install -r requirements.txt
.venv/Scripts/python.exe -m grpc_tools.protoc -I. --python_out=. --grpc_python_out=. datasource.proto
.venv/Scripts/python.exe -m unittest -v
```

测试不连接飞书：使用模拟 HTTP 响应和真实本地 gRPC，覆盖首次/无变化/单项变化/改名/删除、资源子树、分页、令牌刷新、限流、取消、游标损坏和错误不发布快照。
Proto 生成文件无需手工修改；复制目录即包含全部构建输入。

## 4. 无 Docker 的本地调试

先启动插件进程（运行期间保持终端打开）：

```powershell
$env:WEKNORA_PLUGIN_ADDRESS = '127.0.0.1:50071'
.venv/Scripts/python.exe server.py
```

在**独立副本**的 `plugin.yaml` 中，把整个 `spec.runtime` 替换为：

```yaml
  runtime:
    type: grpc
    address: 127.0.0.1:50071
    startupTimeout: 30s
```

其他字段保持原样，特别是 `permissions.network.outbound: true`。
启动同一台机器上的 WeKnora 前，在它的启动终端设置：

```powershell
$env:WEKNORA_PLUGIN_DIRS = (Resolve-Path '../my-feishu-plugin').Path
# 在此终端按项目现有开发说明启动 WeKnora
```

Linux 使用 `export WEKNORA_PLUGIN_DIRS=/absolute/path/my-feishu-plugin`。
如果宿主已有其他插件目录，请追加而非覆盖（Windows 用 `;`，Linux 用 `:`）。
这里的 TCP 服务不受容器隔离，只应绑定本机用于开发；宿主停用只断开外部服务连接，手动启动的 Python 进程仍由开发者管理。
`runtime.address` 模式用于目录发现，不能把 Python 源码或这种清单直接作为管理员上传的本机制品。

## 5. OCI 制品安装

### Windows 页面上传安装（无需 Docker 或 Python）

Windows x64 构建包为 `dist/feishu-wiki-windows-x64-0.1.0.zip`。
管理员在插件管理页上传此 ZIP，再点击启用；随后在知识库中添加
“飞书知识库（外部插件示例）”，在表单填写应用凭据。
ZIP 包含 `plugin.yaml`、`bin/weknora-feishu-wiki.exe` 和配套运行库，勿只上传 EXE。
运行库已随包提供，使用者不需要另行启动 Python。宿主管理进程启停，监听本机 `50079` 端口。
此 Windows 进程模式允许联网，不提供 OCI 的文件系统隔离。
若以前通过目录发现装载过相同 ID，安装器会拒绝重复安装；应先备份并将旧部署目录移出发现目录，再重启宿主后上传。

开发者在 Windows 的插件虚拟环境中重建和验证：

```powershell
python -m pip install pyinstaller==6.22.2
python build_windows.py
python verify_windows.py
```

已验证从 ZIP 解压后的 EXE 可在 PATH 无 Python 的情况下启动，Health/GetInfo/无效配置检查通过。
目录制品采用单进程启动，以匹配当前宿主停止入口进程的生命周期实现。

### OCI 镜像安装

使用原始 OCI `plugin.yaml`，在独立仓库构建：

```bash
docker build -t example/weknora-feishu-wiki:0.1.0 .
mkdir -p dist
python -c "from zipfile import ZipFile; z=ZipFile('dist/feishu-wiki.zip','w'); z.write('plugin.yaml'); z.close()"
```

Windows 用 `New-Item -ItemType Directory -Force dist` 代替 `mkdir -p dist`。
将镜像预先构建或拉取到**WeKnora 实际使用的 Docker daemon**；ZIP 不包含镜像，也不会替你构建镜像。
跨机器分发时，修改 `runtime.image` 为你的镜像仓库地址，并先完成发布和拉取。

系统管理员在“设置 → 插件管理”上传 `dist/feishu-wiki.zip` 并启用，健康状态应为 `healthy`。
也可将仅含 `plugin.yaml` 的部署目录加入 `WEKNORA_PLUGIN_DIRS` 后重启宿主。
OCI 控制通道由宿主注入 Unix Socket 地址，无需发布 50071 端口。
宿主必须已按框架部署说明具备 Docker API 及共享控制目录支持；普通运行应用的 Docker 环境不一定具备这些条件。

本插件需要联网，声明 `outbound: true`，不申请业务文件读写。API 地址固定为 `open.feishu.cn` 且拒绝重定向，避免凭据被转发；这不是内核域名白名单，框架 v1alpha1 不支持精确的域名网络策略。

## 6. 在 WeKnora 中完成同步

1. 进入知识库的数据源设置，新建“飞书知识库（外部插件示例）”。其类型是 `feishu_wiki_plugin`，与已有内置类型不同。
2. 填写 App ID、App Secret、知识空间 ID。
3. 可选填写 `https://你的租户.feishu.cn`（无结尾斜杠）生成原文链接；留空则不生成猜测的链接。
4. “同步删除”留空或填 `false`；明确接受下节语义后才填 `true`。为兼容当前宿主表单，此配置声明为字符串，RPC 也接受 JSON boolean。
5. 校验并选择页面；勾选父页面包括其后代。RPC `resource_ids` 为空表示整个配置空间；若当前 UI 强制选择资源，请勾选需要的顶层页面。
6. 保存并触发同步，等待同步任务和文档解析/索引完成，再检查文档列表与检索结果。
7. 再同步一次：无修改时应无文档更新。只修改一个 docx 再同步：只有该文档进入宿主后续处理。

RPC 配置形状（说明用途，勿提交真实值）：

```json
{
  "type": "feishu_wiki_plugin",
  "credentials": {"app_id": "cli_example", "app_secret": "replace-locally"},
  "settings": {"space_id": "123456789", "sync_deletions": "false"},
  "resource_ids": ["wiki_node_token"]
}
```

App ID/Secret 在 Manifest 中标为 `writeOnly`，宿主把它们放入 `credentials`，其余字段进入 `settings`。

## 7. 增量、删除和恢复约定

游标保存 `connector_cursor.version/scope/files`，其中 files 是 `node_token -> SHA-256`。
游标归宿主保存并于下轮回传，不包含 Secret、access token 或正文。
插件先完成分页遍历及全部需要的正文读取，再发布变化事件，最后发送唯一 `final_cursor_json`。
正文读取、权限或分页失败会使本轮 RPC 失败，不发布删除事件或新游标。
发送阶段若连接中断，宿主可能已接收部分更新；重试会再次发送这些更新，稳定的 `external_id` 用于宿主幂等处理。

删除默认关闭：消失节点保留旧哈希，重新出现时仍能比较。
开启删除后，完整成功扫描中不再可见的已同步文档会发送 `is_deleted=true`。
**飞书列表中的不可见不一定是物理删除，也可能是权限变化或移出所选子树。** 本示例无法区分它们。
如果显式选择的根节点本身消失，则报错而非批量删除。API 返回错误也不会被当成空列表。
改变应用、空间、资源选择、链接地址或删除策略后，旧游标会被拒绝；建议新建数据源。
如选择宿主全量重同步，旧范围数据是否清理由宿主决定，应核对文档列表，不能假设插件会自动删除旧范围。

## 8. 不启动 WeKnora 的真实飞书联调

先安装依赖并生成 Proto。在本地环境设置 `FEISHU_APP_ID`、`FEISHU_APP_SECRET`、`FEISHU_SPACE_ID`；可选 `FEISHU_NODE_TOKENS` 为逗号分隔的页面 token。
建议通过本地 Secret 管理方式注入环境，不在共享终端或聊天中粘贴凭据。

```bash
python smoke_live.py
python smoke_live.py --pause
```

第一条执行两轮，预期第二轮 `emitted_documents=0`；第二条在两轮之间暂停，修改一个所选 docx 后继续，预期第二轮为 1。
脚本只读取飞书，启动本地 gRPC，不打印正文或凭据，也不把文档写入 WeKnora。
它验证真实 API 到插件协议，不能替代第 6 节的宿主入库、解析、向量化和检索验收。

## 9. 如何改造成另一个软件的插件

| 文件/边界 | 开发者需要改什么 |
| --- | --- |
| `plugin.yaml` 与 `server.py` 常量 | 同步修改插件 ID、版本和扩展 ID；更换镜像名称与表单字段 |
| `feishu.py:configuration` | 读取该软件需要的凭据与普通配置，验证参数 |
| `Client` | 替换鉴权、资源分页、父子关系和正文获取；保留失败即中止及有限重试 |
| `synchronize` | 使用该软件的稳定 ID、明确变更检测和删除语义，维护可恢复游标 |
| `server.py` | 保留五个标准 RPC、Health、配置传递、最终游标及错误传播 |
| `datasource.proto` | 保持公开协议，勿通过随意改字段适配业务 |
| `test_plugin.py` | 替换模拟 API，覆盖真实的软件边界与相同增量断言 |

先用模拟数据打通契约，再用只读真实账户联调，最后进入 WeKnora 验证完整知识处理链。
不要让插件直接操作宿主数据库、向量库或导入 `internal/`。新软件的凭据与资源边界必须在自己的实现中验证。

## 10. 排障与验收状态

| 现象 | 排查方向 |
| --- | --- |
| 插件不可见 | 是否运行已包含插件框架的 WeKnora；目录/启用状态、版本范围、GetInfo 身份是否一致 |
| 健康正常但配置校验失败 | Health 只说明进程存活；检查应用发布、App Secret、API scope、空间授权和 space_id |
| 有目录无正文 | 检查 docx 类型及文档内容读取权限；表格等类型不会导入 |
| 177… / 999… 错误码 | 按飞书官方错误码及授权范围排查；插件错误仅暴露状态码，不转发上游消息或凭据 |
| 游标范围不匹配 | 新建数据源或按第 7 节执行全量重同步 |
| OCI 无法启动 | 确认镜像在宿主 daemon、Docker API 可用、Unix Socket 共享目录可达 |

2026-09-09 已把模板复制到主仓外临时目录，重新生成 Proto，16 项模拟 HTTP + 真实 gRPC 自动化测试全部通过（Windows / Python 3.13）。真实租户同步需要使用者提供有效授权；未把模拟测试声称为真实飞书或 WeKnora 全链路验收。
本机未启动 Docker daemon，OCI 构建与运行待 Docker 环境验证；主仓 CI 已加入独立构建与测试步骤。
