# 本机受控联网验证（2026-09-11）

入口：http://127.0.0.1:5174 。本机宿主已重新编译、替换并启动。

## 已准备的数据

- 插件“飞书公开链接导入”0.2.0：Python 编译 EXE，操作系统禁止直接联网，已批准宿主受控 HTTP。
- 插件“飞书公开链接（禁网验证）”0.1.0：未申请宿主 HTTP，用于相同链接的失败对照。
- 知识库“飞书受控联网验证”：数据源“飞书公开链接受控同步”，已填写用户提供的链接。
- 旧宿主程序与旧公开链接插件备份在 `artifacts/upgrade-backup-20260911`。

## 页面操作

1. 刷新页面，进入设置 → 插件管理，检查公开链接插件版本为 0.2.0、运行中，查看允许域名、GET 方法与限额。
2. 打开“飞书受控联网验证”知识库，进入数据源，编辑“飞书公开链接受控同步”并测试连接：应成功。
3. 点击立即同步，查看同步日志与文档。再次同步且网页没有变化时，应不再新增文档。
4. 新增数据源时选择“飞书公开链接（禁网验证）”，填写相同链接并测试连接：应失败，无需保存。
5. 选择受控公开链接插件，输入 `https://unapproved.feishu.cn/wiki/test` 测试：应返回 `DOMAIN_NOT_ALLOWED`，无需保存。

原链接：`https://docs.feishu.cn/article/wiki/ACKgw651xiDd3WkiTT8cmDhBnDg`。

## 验收证据与边界

- 28 项模板 Python 测试、2 项 Python HTTP SDK 测试通过。
- Go/Python 真实管道测试覆盖并发二进制大消息、流量控制、取消与反向 HTTP。
- `artifacts/python-public-native-acceptance.log`：真实 Windows 受限 Python EXE 的安装、批准、正文读取、增量和启停验收。
- `artifacts/feishu-public-live-acceptance.json`：运行中应用的测试连接成功、未授权域名返回 400。
- `artifacts/feishu-no-network-live-acceptance.json`：同一链接在完全禁网插件上返回 400。
- `artifacts/feishu-public-live-sync-acceptance.json`：首次同步新增 1 篇、第二次新增与更新均为 0；2549 字节正文解析和摘要均 completed，混合检索成功返回 3 条结果。
- 此次内嵌浏览器控制多次发生 CDP 超时，部署后的验证使用应用正常登录和管理 API，不能算作页面逐项点击验收。
- Windows WFP 违规事件订阅仍受当前账户权限限制；原生网络隔离已验证，不能宣称所有网络尝试均已写入系统审计。OCI 实机验收不在本次 Windows 验收范围内。

本包只允许声明中的五个精确飞书域名，其他租户需要管理员重新批准新版声明。它不承诺读取所有飞书页面；需要登录、分页正文不完整等情况仍明确报错。

## 交付物

`artifacts/plugins/feishu-public-windows-x64-0.2.0.zip` 及同名 `.sha256` 是可上传的编译插件包。
开发接入指南见 [受控 HTTP](CONTROLLED-HTTP.md) 和 [Python 飞书模板](templates/feishu-wiki-python/PUBLIC_LINKS.md)。
