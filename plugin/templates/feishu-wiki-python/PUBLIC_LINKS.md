# 飞书公开链接导入（Windows）

无需 App ID、App Secret、space_id。此插件使用公开网页匿名访客会话读取正文，
不读取浏览器 Cookie，不提交登录信息，不访问需授权的内部 API。
它与企业应用插件使用不同 ID，可以同时安装。

## 安装与使用

1. 在 WeKnora 插件管理上传 `feishu-public-windows-x64-0.2.0.zip`，核对受控 HTTP 权限并批准启用，确认健康。
2. 打开目标知识库，添加数据源 **飞书公开链接（无需应用凭据）**。
3. 填写名称及“公开飞书页面链接”，多个页面用英文逗号分隔，最多 20 个。
4. 测试连接，选择页面，保存并触发同步。
5. 每次同步重新匿名读取页面，标题/正文哈希不变则不发送更新。

已实测的公开链接（2026-09-10，分享者后续可能更改权限）：

[公开页面示例](https://docs.feishu.cn/article/wiki/ACKgw651xiDd3WkiTT8cmDhBnDg)

这是网页读取技术的测试样本，不是对页面内容的推荐。
用户此前提供的 `my.feishu.cn/wiki/VUkxwcZyGirINSkjnsZcMZ4dnbt` 在匿名请求时
停留于登录页面；需所有者按飞书实际支持的分享设置开放匿名访问，否则本插件不能导入。

## 范围与限制

- 仅处理所填单篇 wiki/docx 页面，不自动列出/遍历整棵知识库。
- 提取页面返回的普通文字块，按正文树顺序组织；不保留样式，不下载图片、附件，
  不保证表格、内嵌文档、公式和引用标签的完整还原。
- 依赖飞书网页嵌入的 `clientVars` JSON 格式，这是网页实现细节，并非稳定开放 API。
- 如果 `has_more` 表明正文仍有分页，或子块缺失，则拒绝导入，避免静默截断。
  通往 AGI 之路首页在本次验证中属于分页长页面，应选择较短的单篇页面测试。
- 需要登录、密码或访问申请的页面会报错；公开权限被收回后，同步失败并保留旧游标，
  不会误发删除事件。已入库文档需使用者手动管理。
- 允许官方账号站点建立匿名访客会话后返回正文，但不会填写登录表单。
  v0.2.0 的宿主权限声明允许 `accounts.feishu.cn` 和 `login.feishu.cn` 中转。
  直接输入登录地址仍被拒绝，最终停留登录页也会报错。
- 输入链接只接受 HTTPS 飞书 wiki/docx 路径；宿主逐跳检查目标域名和公开 IP。
  本包精确允许 docs.feishu.cn、www.feishu.cn、xiaobot123.feishu.cn、accounts.feishu.cn、login.feishu.cn。
  其他租户域名需要修改声明、重新打包并由管理员批准，不能由用户输入链接扩权。
- 图片和其他非文字块不会进入导入内容；没有文字正文时明确报错。
- 配置/选择范围改变后需要新建数据源或全量同步，不自动删除旧范围文档。
- Windows EXE 使用原生受限身份，禁止直接联网。Python SDK 通过 stdio 调用宿主 HTTP；
  无通道或权限拒绝时直接失败，不回退到 urllib、requests 或本地 TCP 代理。

## 开发与验证

实现文件：`public_feishu.py`（URL/匿名访问/正文解析/增量）、`public_server.py`
（标准 Datasource RPC）、`plugin.public.yaml`（独立插件描述文件）。

在已配置的 Python 虚拟环境中执行：

```powershell
python -m unittest discover -q
python build_windows.py --public
# 真实公开网页到受限 EXE 的宿主验收，不写入现有知识库
$env:FEISHU_PUBLIC_TEST_URL = 'https://docs.feishu.cn/article/wiki/ACKgw651xiDd3WkiTT8cmDhBnDg'
python verify_windows.py --public
```

原生验收 `TestWindowsNativePythonPublic` 已通过：安装、批准、受限 EXE 启动、
真实页面 Validate/List/Fetch、首次 1 篇、第二次 0 篇、未授权目标拒绝和重新启用。
日志见主仓 `artifacts/python-public-native-acceptance.log`。这项测试不代替 Embedding/检索全链路验收。

## 从 0.1.x 升级到 0.2.0

插件 ID 和协议未改变，已有数据源与游标可继续使用。当前宿主不支持通过重复上传覆盖安装。
先在页面停用“飞书公开链接导入”，然后停止本地服务，将旧目录备份移出插件扫描目录，
再启动服务并上传新版 ZIP。不要删除知识库或重建数据源。
确认插件版本显示 0.2.0，批准受控 HTTP 权限后再同步。保持原数据源链接不变可继续使用原游标；
如果更改链接地址（即使指向同一页面），当前 URL 身份策略仍将其视为新资源。
